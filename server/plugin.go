package main

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-ai/server/ai"
	"github.com/mattermost/mattermost-plugin-ai/server/ai/anthropic"
	"github.com/mattermost/mattermost-plugin-ai/server/ai/asksage"
	"github.com/mattermost/mattermost-plugin-ai/server/ai/openai"
	"github.com/mattermost/mattermost-plugin-ai/server/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/server/metrics"
	"github.com/mattermost/mattermost-plugin-ai/server/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/shared/httpservice"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"strings"
)

const (
	BotUsername = "ai"

	CallsRecordingPostType = "custom_calls_recording"
	CallsBotUsername       = "calls"
	ZoomBotUsername        = "zoom"

	ffmpegPluginPath = "./plugins/mattermost-ai/server/dist/ffmpeg"
)

//go:embed ai/prompts
var promptsFolder embed.FS

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	pluginAPI *pluginapi.Client

	telemetry    *telemetry.Client
	telemetryMut sync.RWMutex

	ffmpegPath string

	db      *sqlx.DB
	builder sq.StatementBuilderType

	prompts *ai.Prompts

	streamingContexts      map[string]PostStreamContext
	streamingContextsMutex sync.Mutex

	licenseChecker *enterprise.LicenseChecker
	metricsService metrics.Metrics
	metricsHandler http.Handler

	botsLock sync.RWMutex
	bots     []*Bot

	i18n *i18n.Bundle

	llmUpstreamHTTPClient *http.Client
}

func resolveffmpegPath() string {
	_, standardPathErr := exec.LookPath("ffmpeg")
	if standardPathErr != nil {
		_, pluginPathErr := exec.LookPath(ffmpegPluginPath)
		if pluginPathErr != nil {
			return ""
		}
		return ffmpegPluginPath
	}

	return "ffmpeg"
}

func (p *Plugin) OnActivate() error {
	p.pluginAPI = pluginapi.NewClient(p.API, p.Driver)

	p.licenseChecker = enterprise.NewLicenseChecker(p.pluginAPI)

	// Register the ai create-team command
	if err := p.API.RegisterCommand(&model.Command{
		Trigger:          "ai",
		AutoComplete:     true,
		AutoCompleteDesc: "AI-powered commands",
		AutoCompleteHint: "[command]",
		AutocompleteData: &model.AutocompleteData{
			Trigger: "ai",
			SubCommands: []*model.AutocompleteData{
				{
					Trigger:     "create-team",
					Hint:        "[team description]",
					HelpText:    "Create a team with AI-suggested channels based on description",
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to register command: %w", err)
	}

	p.metricsService = metrics.NewMetrics(metrics.InstanceInfo{
		InstallationID: os.Getenv("MM_CLOUD_INSTALLATION_ID"),
		PluginVersion:  manifest.Version,
	})
	p.metricsHandler = metrics.NewMetricsHandler(p.GetMetrics())

	p.i18n = i18nInit()

	p.llmUpstreamHTTPClient = httpservice.MakeHTTPServicePlugin(p.API).MakeClient(true)
	p.llmUpstreamHTTPClient.Timeout = time.Minute * 10 // LLM requests can be slow

	if err := p.MigrateServicesToBots(); err != nil {
		p.pluginAPI.Log.Error("failed to migrate services to bots", "error", err)
		// Don't fail on migration errors
	}

	if err := p.EnsureBots(); err != nil {
		p.pluginAPI.Log.Error("Failed to ensure bots", "error", err)
		// Don't fail on ensure bots errors as this leaves the plugin in an awkward state
		// where it can't be configured from the system console.
	}

	if err := p.SetupDB(); err != nil {
		return err
	}

	var err error
	p.prompts, err = ai.NewPrompts(promptsFolder)
	if err != nil {
		return err
	}

	p.ffmpegPath = resolveffmpegPath()
	if p.ffmpegPath == "" {
		p.pluginAPI.Log.Error("ffmpeg not installed, transcriptions will be disabled.", "error", err)
	}

	p.streamingContexts = map[string]PostStreamContext{}

	return nil
}

func (p *Plugin) OnDeactivate() error {
	if err := p.uninitTelemetry(); err != nil {
		p.API.LogError(err.Error())
	}
	return nil
}

func (p *Plugin) getLLM(llmBotConfig ai.BotConfig) ai.LanguageModel {
	llmMetrics := p.metricsService.GetMetricsForAIService(llmBotConfig.Name)

	var llm ai.LanguageModel
	switch llmBotConfig.Service.Type {
	case "openai":
		llm = openai.New(llmBotConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	case "openaicompatible":
		llm = openai.NewCompatible(llmBotConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	case "azure":
		llm = openai.NewAzure(llmBotConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	case "anthropic":
		llm = anthropic.New(llmBotConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	case "asksage":
		llm = asksage.New(llmBotConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	}

	cfg := p.getConfiguration()
	if cfg.EnableLLMTrace {
		llm = NewLanguageModelLogWrapper(p.pluginAPI.Log, llm)
	}

	llm = NewLLMTruncationWrapper(llm)

	return llm
}

func (p *Plugin) getTranscribe() ai.Transcriber {
	cfg := p.getConfiguration()
	var botConfig ai.BotConfig
	for _, bot := range cfg.Bots {
		if bot.Name == cfg.TranscriptGenerator {
			botConfig = bot
			break
		}
	}
	llmMetrics := p.metricsService.GetMetricsForAIService(botConfig.Name)
	switch botConfig.Service.Type {
	case "openai":
		return openai.New(botConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	case "openaicompatible":
		return openai.NewCompatible(botConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	case "azure":
		return openai.NewAzure(botConfig.Service, p.llmUpstreamHTTPClient, llmMetrics)
	}
	return nil
}

var (
	// ErrNoResponse is returned when no response is posted under a normal condition.
	ErrNoResponse = errors.New("no response")
)

func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	if err := p.handleMessages(post); err != nil {
		if errors.Is(err, ErrNoResponse) {
			p.pluginAPI.Log.Debug(err.Error())
		} else {
			p.pluginAPI.Log.Error(err.Error())
		}
	}
}

const (
	ActivateAIProp  = "activate_ai"
	FromWebhookProp = "from_webhook"
	FromBotProp     = "from_bot"
	FromPluginProp  = "from_plugin"
	WranglerProp    = "wrangler"
)

func (p *Plugin) handleMessages(post *model.Post) error {
	// Don't respond to ourselves
	if p.IsAnyBot(post.UserId) {
		return fmt.Errorf("not responding to ourselves: %w", ErrNoResponse)
	}

	// Never respond to remote posts
	if post.RemoteId != nil && *post.RemoteId != "" {
		return fmt.Errorf("not responding to remote posts: %w", ErrNoResponse)
	}

	// Wranger posts should be ignored
	if post.GetProp(WranglerProp) != nil {
		return fmt.Errorf("not responding to wrangler posts: %w", ErrNoResponse)
	}

	// Don't respond to plugins unless they ask for it
	if post.GetProp(FromPluginProp) != nil && post.GetProp(ActivateAIProp) == nil {
		return fmt.Errorf("not responding to plugin posts: %w", ErrNoResponse)
	}

	// Don't respond to webhooks
	if post.GetProp(FromWebhookProp) != nil {
		return fmt.Errorf("not responding to webhook posts: %w", ErrNoResponse)
	}

	channel, err := p.pluginAPI.Channel.Get(post.ChannelId)
	if err != nil {
		return fmt.Errorf("unable to get channel: %w", err)
	}

	postingUser, err := p.pluginAPI.User.Get(post.UserId)
	if err != nil {
		return err
	}

	// Don't respond to other bots unless they ask for it
	if (postingUser.IsBot || post.GetProp(FromBotProp) != nil) && post.GetProp(ActivateAIProp) == nil {
		return fmt.Errorf("not responding to other bots: %w", ErrNoResponse)
	}

	// Check we are mentioned like @ai
	if bot := p.GetBotMentioned(post.Message); bot != nil {
		return p.handleMentions(bot, post, postingUser, channel)
	}

	// Check if this is post in the DM channel with any bot
	if bot := p.GetBotForDMChannel(channel); bot != nil {
		return p.handleDMs(bot, channel, postingUser, post)
	}

	return nil
}

func (p *Plugin) handleMentions(bot *Bot, post *model.Post, postingUser *model.User, channel *model.Channel) error {
	if err := p.checkUsageRestrictions(postingUser.Id, channel); err != nil {
		return err
	}

	p.track(evAIBotMention, map[string]any{
		"actual_user_id":   postingUser.Id,
		"bot_id":           bot.mmBot.UserId,
		"bot_service_type": bot.cfg.Service.Type,
	})

	if err := p.processUserRequestToBot(bot, p.MakeConversationContext(bot, postingUser, channel, post)); err != nil {
		return fmt.Errorf("unable to process bot mention: %w", err)
	}

	return nil
}

func (p *Plugin) handleDMs(bot *Bot, channel *model.Channel, postingUser *model.User, post *model.Post) error {
	if err := p.checkUsageRestrictionsForUser(postingUser.Id); err != nil {
		return err
	}

	if post.RootId == "" {
		p.track(evUserStartedConversation, map[string]any{
			"user_actual_id":   postingUser.Id,
			"bot_id":           bot.mmBot.UserId,
			"bot_service_type": bot.cfg.Service.Type,
		})
	} else {
		p.track(evContinueConversation, map[string]any{
			"user_actual_id":   postingUser.Id,
			"bot_id":           bot.mmBot.UserId,
			"bot_service_type": bot.cfg.Service.Type,
		})
	}

	if err := p.processUserRequestToBot(bot, p.MakeConversationContext(bot, postingUser, channel, post)); err != nil {
		return fmt.Errorf("unable to process bot DM: %w", err)
	}

	return nil
}
func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	split := strings.Fields(args.Command)
	if len(split) < 2 {
		return &model.CommandResponse{
			Text: "Invalid command. Use `/ai help` to see available commands.",
			ResponseType: model.CommandResponseTypeEphemeral,
		}, nil
	}

	command := split[1]

	switch command {
	case "create-team":
		return p.executeCreateTeam(args, split[2:])
	default:
		return &model.CommandResponse{
			Text: "Unknown command. Use `/ai help` to see available commands.",
			ResponseType: model.CommandResponseTypeEphemeral,
		}, nil
	}
}

func (p *Plugin) createTeamAsync(args *model.CommandArgs, teamName string, description string, bot *Bot) {

	// Create team creator with bot's LLM
	teamCreator := ai.NewTeamCreator(p.getLLM(bot.cfg))

	// Notify user that we're working on channel suggestions
	p.API.SendEphemeralPost(args.UserId, &model.Post{
		ChannelId: args.ChannelId,
		Message:   "Working on creating your team with AI-suggested channels...",
	})

	// Get channel suggestions
	channels, err := teamCreator.SuggestChannels(description)
	if err != nil {
		p.API.SendEphemeralPost(args.UserId, &model.Post{
			ChannelId: args.ChannelId,
			Message:   "Failed to get channel suggestions from AI: " + err.Error(),
		})
		return
	}

	// Use provided team name as display name
	displayName := teamName
	// Create URL-friendly team name
	urlName := strings.ToLower(strings.ReplaceAll(teamName, " ", "-"))

	// Create the team
	team, appErr := p.API.CreateTeam(&model.Team{
		Name:        urlName,
		DisplayName: displayName,
		Type:        model.TeamOpen,
	})
	if appErr != nil {
		p.API.SendEphemeralPost(args.UserId, &model.Post{
			ChannelId: args.ChannelId,
			Message:   "Failed to create team: " + appErr.Error(),
		})
		return
	}

	// Add creator to team
	_, appErr = p.API.CreateTeamMember(team.Id, args.UserId)
	if appErr != nil {
		p.API.SendEphemeralPost(args.UserId, &model.Post{
			ChannelId: args.ChannelId,
			Message:   "Failed to add you to team: " + appErr.Error(),
		})
		return
	}

	// Create channels
	var successfulChannels []string
	var failedChannels []string

	for _, suggestion := range channels {
		channelType := model.ChannelTypeOpen
		if suggestion.Private {
			channelType = model.ChannelTypePrivate
		}

		channel, appErr := p.API.CreateChannel(&model.Channel{
			TeamId:      team.Id,
			Type:        channelType,
			Name:        suggestion.Name,
			DisplayName: suggestion.DisplayName,
			Purpose:     suggestion.Purpose,
			Header:      suggestion.Header,
		})

		if appErr != nil {
			p.API.LogError("Failed to create channel", "channel", suggestion.Name, "error", appErr)
			failedChannels = append(failedChannels, suggestion.Name)
			continue
		}

		// Add the user to the channel
		_, appErr = p.API.AddChannelMember(channel.Id, args.UserId)
		if appErr != nil {
			p.API.LogError("Failed to add user to channel", "channel", suggestion.Name, "error", appErr)
		}

		successfulChannels = append(successfulChannels, suggestion.Name)
	}

	response := fmt.Sprintf("✅ Team %s is ready!\nCreated %d channels: %s", team.DisplayName, len(successfulChannels), strings.Join(successfulChannels, ", "))
	if len(failedChannels) > 0 {
		response += fmt.Sprintf("\nFailed to create channels: %s", strings.Join(failedChannels, ", "))
	}

	p.API.SendEphemeralPost(args.UserId, &model.Post{
		ChannelId: args.ChannelId,
		Message:   response,
	})
}
func (p *Plugin) executeCreateTeam(args *model.CommandArgs, arguments []string) (*model.CommandResponse, *model.AppError) {
	if len(arguments) < 2 {
		return &model.CommandResponse{
			Text: "Please provide a team name and description. Usage: /ai create-team [team-name] [description]",
			ResponseType: model.CommandResponseTypeEphemeral,
		}, nil
	}

	teamName := arguments[0]
	description := strings.Join(arguments[1:], " ")

	// Get a bot for AI operations
	var bot *Bot
	bot = p.GetBotForDMChannel(&model.Channel{Id: args.ChannelId})
	if bot == nil {
		// If no DM bot found, use the first configured bot
		p.botsLock.RLock()
		if len(p.bots) > 0 {
			bot = p.bots[0]
		}
		p.botsLock.RUnlock()
		
		if bot == nil {
			return &model.CommandResponse{
				Text: "No AI service configured. Please configure at least one bot in the plugin settings.",
				ResponseType: model.CommandResponseTypeEphemeral,
			}, nil
		}
	}

	// Start async team creation
	go p.createTeamAsync(args, teamName, description, bot)

	return &model.CommandResponse{
		Text: fmt.Sprintf("Starting team creation for '%s'... I'll notify you when it's ready!", teamName),
		ResponseType: model.CommandResponseTypeEphemeral,
	}, nil
}
