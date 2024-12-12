package ai

import (
	"fmt"
	"strings"
)

type TeamCreator struct {
	llm LanguageModel
}

func NewTeamCreator(llm LanguageModel) *TeamCreator {
	return &TeamCreator{
		llm: llm,
	}
}

func (tc *TeamCreator) SuggestChannels(teamDescription string) ([]string, error) {
	prompt := fmt.Sprintf(`Given this team description: "%s"

Please suggest a list of 5-8 channels that would be useful for this team. Return only channel names separated by newlines.
Channel names must:
- Be lowercase
- Use hyphens instead of spaces
- Be descriptive but concise
- Follow Mattermost channel naming conventions

Example format:
general
announcements
team-updates`, teamDescription)

	result, err := tc.llm.ChatCompletionNoStream(BotConversation{
		Posts: []Post{
			{
				Role:    PostRoleUser,
				Message: prompt,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get channel suggestions: %w", err)
	}

	// Split response into lines and clean up
	channels := []string{}
	for _, line := range strings.Split(result, "\n") {
		channel := strings.TrimSpace(line)
		if channel != "" {
			channels = append(channels, channel)
		}
	}

	return channels, nil
}
