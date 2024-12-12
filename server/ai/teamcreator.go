package ai

import (
	"encoding/json"
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

type ChannelSuggestion struct {
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Header      string `json:"header"`
	Private     bool   `json:"private"`
	DisplayName string `json:"displayName"`
}

func (tc *TeamCreator) SuggestChannels(teamDescription string) ([]ChannelSuggestion, error) {
	prompt := fmt.Sprintf(`Given this team description: "%s"

Please suggest a list of 5-8 channels that would be useful for this team. Return the response in JSON format.
Each channel must have:
- name: lowercase with hyphens instead of spaces
- purpose: brief description of the channel's purpose
- header: welcome message or description shown at top of channel
- private: boolean indicating if it should be private
- displayName: human readable name with proper capitalization

Return format must be a JSON array of objects like:
[
  {
    "name": "channel-slug",
    "purpose": "Channel purpose description",
    "header": "Welcome! This channel is for...",
    "private": false,
    "displayName": "Channel Display Name"
  }
]`, teamDescription)

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

	var suggestions []ChannelSuggestion
	if err := json.Unmarshal([]byte(result), &suggestions); err != nil {
		return nil, fmt.Errorf("failed to parse channel suggestions: %w", err)
	}

	return suggestions, nil
}
