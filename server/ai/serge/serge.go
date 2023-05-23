package serge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/crspeller/mattermost-plugin-summarize/server/ai"
	sse "github.com/r3labs/sse/v2"
)

type Serge struct {
	model   string
	baseUrl string
}

const (
	ChatMessageRoleSystem    = "system"
	ChatMessageRoleUser      = "user"
	ChatMessageRoleAssistant = "assistant"
)

func New(baseUrl string, model string) *Serge {
	return &Serge{
		baseUrl: baseUrl,
		model:   model,
	}
}

type ChatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest represents a request structure for chat completion API.
type ChatCompletionRequest struct {
	Model            string                  `json:"model"`
	Messages         []ChatCompletionMessage `json:"messages"`
	MaxTokens        int                     `json:"max_tokens,omitempty"`
	Temperature      float32                 `json:"temperature,omitempty"`
	TopP             float32                 `json:"top_p,omitempty"`
	N                int                     `json:"n,omitempty"`
	Stream           bool                    `json:"stream,omitempty"`
	Stop             []string                `json:"stop,omitempty"`
	PresencePenalty  float32                 `json:"presence_penalty,omitempty"`
	FrequencyPenalty float32                 `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]int          `json:"logit_bias,omitempty"`
	User             string                  `json:"user,omitempty"`
	InitPrompt       string                  `json:"init_prompt,omitempty"`
}

func conversationToCompletion(conversation ai.BotConversation) []ChatCompletionMessage {
	result := make([]ChatCompletionMessage, 0, len(conversation.Posts))

	for _, post := range conversation.Posts {
		role := ChatMessageRoleUser
		if post.Role == ai.PostRoleBot {
			role = ChatMessageRoleAssistant
		}
		result = append(result, ChatCompletionMessage{
			Role:    role,
			Content: post.Message,
		})
	}

	return result
}

func (s *Serge) ThreadCompletion(systemMessage string, conversation ai.BotConversation) (*ai.TextStreamResult, error) {
	request := ChatCompletionRequest{
		Model:      s.model,
		InitPrompt: systemMessage,
		Messages:   conversationToCompletion(conversation),
		Stream:     true,
	}

	return s.createChatCompletionStream(context.Background(), request)
}

func (s *Serge) ContinueQuestionThread(posts ai.BotConversation) (*ai.TextStreamResult, error) {
	return s.ThreadCompletion(GenericQuestionSystemMessage, posts)
}

func (s *Serge) createChatCompletionStream(ctx context.Context, request ChatCompletionRequest) (*ai.TextStreamResult, error) {
	u, err := url.Parse(s.baseUrl + "/chat")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("model", s.model)
	q.Set("init_prompt", request.InitPrompt)
	u.RawQuery = q.Encode()
	resp, err := http.Post(u.String(), "application/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	chatIDBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var chatID string
	err = json.Unmarshal(chatIDBytes, &chatID)
	if err != nil {
		return nil, err
	}

	u, err = url.Parse(s.baseUrl + "/chat/" + chatID + "/question")
	if err != nil {
		return nil, err
	}

	prompt := ""
	for _, message := range request.Messages {
		prompt += message.Content
		prompt += "\n"
	}

	q = u.Query()
	q.Set("prompt", prompt)
	u.RawQuery = q.Encode()

	stream := make(chan string)

	go func() {
		defer close(stream)
		close := make(chan struct{})

		client := sse.NewClient(u.String())
		client.Subscribe("message", func(msg *sse.Event) {
			fmt.Println(msg)
			stream <- string(msg.Data)
		})
		client.Subscribe("close", func(msg *sse.Event) {
			fmt.Println(msg)
			close <- struct{}{}
		})

		<-close
	}()

	return &ai.TextStreamResult{Stream: stream}, nil
}

func (s *Serge) createChatCompletion(ctx context.Context, request ChatCompletionRequest) (string, error) {
	u, err := url.Parse(s.baseUrl + "/chat")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("model", s.model)
	q.Set("init_prompt", request.InitPrompt)
	u.RawQuery = q.Encode()
	resp, err := http.Post(u.String(), "application/json", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	chatIDBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var chatID string
	err = json.Unmarshal(chatIDBytes, &chatID)
	if err != nil {
		return "", err
	}

	u, err = url.Parse(s.baseUrl + "/chat/" + chatID + "/question")
	if err != nil {
		return "", err
	}

	prompt := ""
	for _, message := range request.Messages {
		prompt += message.Content
		prompt += "\n"
	}

	q = u.Query()
	q.Set("prompt", prompt)
	u.RawQuery = q.Encode()

	resp, err = http.Post(u.String(), "application/json", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var response string

	err = json.Unmarshal(responseBytes, &response)
	if err != nil {
		return "", err
	}

	fmt.Println("GENERATED RESPONSE", response)
	return strings.TrimSpace(response), nil
}

func (s *Serge) SummarizeThread(thread string) (*ai.TextStreamResult, error) {
	request := ChatCompletionRequest{
		Model: s.model,
		Messages: []ChatCompletionMessage{
			{
				Role:    ChatMessageRoleSystem,
				Content: SummarizeThreadSystemMessage,
			},
			{
				Role:    ChatMessageRoleUser,
				Content: thread,
			},
		},
		Stream: true,
	}
	return s.createChatCompletionStream(context.Background(), request)
}

func (s *Serge) ContinueThreadInterrogation(thread string, posts ai.BotConversation) (*ai.TextStreamResult, error) {
	reqeust := ChatCompletionRequest{
		Model:      s.model,
		InitPrompt: AnswerThreadQuestionSystemMessage,
		Messages: append(
			[]ChatCompletionMessage{
				{
					Role:    ChatMessageRoleUser,
					Content: thread,
				},
			},
			conversationToCompletion(posts)...,
		),
		Stream: true,
	}

	return s.createChatCompletionStream(context.Background(), reqeust)
}

func (s *Serge) SelectEmoji(message string) (string, error) {
	resp, err := s.createChatCompletion(
		context.Background(),
		ChatCompletionRequest{
			Model:     s.model,
			MaxTokens: 25,
			Messages: []ChatCompletionMessage{
				{
					Role:    ChatMessageRoleSystem,
					Content: EmojiSystemMessage,
				},
				{
					Role:    ChatMessageRoleUser,
					Content: message,
				},
			},
		},
	)
	if err != nil {
		return "", err
	}
	result := strings.Trim(strings.TrimSpace(resp), ":")

	return result, nil
}
