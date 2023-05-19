package mattermostai

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"

	"github.com/crspeller/mattermost-plugin-summarize/server/ai"
	"github.com/gorilla/websocket"
)

const (
	ChatMessageRoleSystem    = "system"
	ChatMessageRoleUser      = "user"
	ChatMessageRoleAssistant = "assistant"
)

type MattermostAI struct {
	url    string
	secret string
	model  string
}

func New(url string, secret string) *MattermostAI {
	return &MattermostAI{
		url:    url,
		secret: secret,
	}
}

type ImageQueryRequest struct {
	Prompt string `json:"prompt"`
}

type TextQueryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type TextQueryRequest struct {
	BotDescription string             `json:"bot_description"`
	Messages       []TextQueryMessage `json:"messages"`
}

type EmojiQueryRequest struct {
	Prompt string `json:"prompt"`
}

type TextQueryResponse struct {
	Response string `json:"response"`
}

func (s *MattermostAI) SummarizeThread(thread string) (*ai.TextStreamResult, error) {
	botDescription := `You are a helpful assistant that summarizes threads. Given a thread, return a short summary of the thread. Do not refer to the thread, just give the summary. Include who was speaking. Then answer any questions the user has about the thread. Keep your responses short.
`
	requestBody, err := json.Marshal(TextQueryRequest{BotDescription: botDescription, Messages: []TextQueryMessage{{Role: ChatMessageRoleUser, Content: thread}}})
	if err != nil {
		return nil, err
	}

	url := strings.Replace(s.url, "http://", "ws://", 1)
	url = strings.Replace(url, "https://", "wss://", 1)
	c, _, err := websocket.DefaultDialer.Dial(url+"/botQueryStream", nil)
	if err != nil {
		return nil, err
	}

	c.WriteMessage(websocket.TextMessage, requestBody)

	stream := make(chan string)

	go func() {
		defer close(stream)
		defer c.Close()
		for {
			messageType, message, err := c.ReadMessage()
			if err != nil {
				break
			}
			if messageType == websocket.CloseMessage {
				break
			}
			if string(message) == "" {
				break
			}
			stream <- string(message)
			stream <- " "
		}
	}()

	return &ai.TextStreamResult{Stream: stream}, nil
}

/*func (s *MattermostAI) AnswerQuestionOnThread(thread string, question string) (string, error) {
	prompt := thread + "\nbot, answer the question about the conversation so far: " + question
	requestBody, err := json.Marshal(TextQueryRequest{Prompt: prompt})
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Post(s.url+"/botQuery", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return "", err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var response TextQueryResponse
	json.Unmarshal(data, &response)

	return response.Response, nil
}*/

func (s *MattermostAI) GenerateImage(prompt string) (image.Image, error) {
	requestBody, err := json.Marshal(ImageQueryRequest{Prompt: prompt})
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Post(s.url+"/generateImage", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	r := bytes.NewReader(data)
	imgData, err := png.Decode(r)
	if err != nil {
		return nil, err
	}

	return imgData, nil
}

func (s *MattermostAI) SelectEmoji(message string) (string, error) {
	requestBody, err := json.Marshal(EmojiQueryRequest{Prompt: message})
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Post(s.url+"/selectEmoji", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return "", err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var response TextQueryResponse
	json.Unmarshal(data, &response)

	return response.Response, nil
}

func (s *MattermostAI) ContinueThreadInterrogation(originalThread string, conversation ai.BotConversation) (*ai.TextStreamResult, error) {
	prompt := originalThread + "\nbot, answer the question about the conversation so far: " // + strings.Join(posts, "\n")
	botDescription := "You are a helpful assistant that answers questions about threads. Give a short answer that correctly answers questions asked."

	requestBody, err := json.Marshal(TextQueryRequest{BotDescription: botDescription, Messages: []TextQueryMessage{{Role: ChatMessageRoleUser, Content: prompt}}})
	if err != nil {
		return nil, err
	}

	url := strings.Replace(s.url, "http://", "ws://", 1)
	url = strings.Replace(url, "https://", "wss://", 1)
	c, _, err := websocket.DefaultDialer.Dial(url+"/botQueryStream", nil)
	if err != nil {
		return nil, err
	}

	c.WriteMessage(websocket.TextMessage, requestBody)

	stream := make(chan string)

	go func() {
		defer close(stream)
		for {
			messageType, message, err := c.ReadMessage()
			if err != nil {
				break
			}
			if messageType == websocket.CloseMessage {
				break
			}
			stream <- string(message)
			stream <- " "
		}
	}()

	return &ai.TextStreamResult{Stream: stream}, nil
}

func conversationToCompletion(conversation ai.BotConversation) []TextQueryMessage {
	result := []TextQueryMessage{}
	for _, post := range conversation.Posts {
		role := ChatMessageRoleUser
		if post.Role == ai.PostRoleBot {
			role = ChatMessageRoleAssistant
		}
		result = append(result, TextQueryMessage{Role: role, Content: post.Message})
	}

	return result
}

func (s *MattermostAI) ContinueQuestionThread(conversation ai.BotConversation) (*ai.TextStreamResult, error) {
	messages := conversationToCompletion(conversation)
	botDescription := "You are a helpful assistant."
	requestBody, err := json.Marshal(TextQueryRequest{BotDescription: botDescription, Messages: messages})
	if err != nil {
		return nil, err
	}

	url := strings.Replace(s.url, "http://", "ws://", 1)
	url = strings.Replace(url, "https://", "wss://", 1)
	c, _, err := websocket.DefaultDialer.Dial(url+"/botQueryStream", nil)
	if err != nil {
		return nil, err
	}

	c.WriteMessage(websocket.TextMessage, requestBody)

	stream := make(chan string)

	go func() {
		defer close(stream)
		for {
			messageType, message, err := c.ReadMessage()
			if err != nil {
				break
			}
			if messageType == websocket.CloseMessage {
				break
			}
			stream <- string(message)
			stream <- " "
		}
	}()

	return &ai.TextStreamResult{Stream: stream}, nil
}
