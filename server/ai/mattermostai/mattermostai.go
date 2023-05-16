package mattermostai

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"

	"github.com/crspeller/mattermost-plugin-summarize/server/ai"
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

type TextQueryRequest struct {
	BotDescription string `json:"bot_description"`
	Prompt         string `json:"prompt"`
}

type TextQueryResponse struct {
	Response string `json:"response"`
}

func (s *MattermostAI) SummarizeThread(thread string) (*ai.TextStreamResult, error) {
	botDescription := `You are a helpful assistant that summarizes threads. Given a thread, return a summary of the thread using less than 30 words. Do not refer to the thread, just give the summary. Include who was speaking. Then answer any questions the user has about the thread. Keep your responses short.
`
	requestBody, err := json.Marshal(TextQueryRequest{BotDescription: botDescription, Prompt: thread})
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Post(s.url+"/botQuery", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response TextQueryResponse
	json.Unmarshal(data, &response)

	return ai.NewStreamFromString(response.Response), nil
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
	requestBody, err := json.Marshal(TextQueryRequest{Prompt: message})
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
	requestBody, err := json.Marshal(TextQueryRequest{Prompt: prompt, BotDescription: "You are a helpful assistant that answers questions about threads. Give a short answer that correctly answers questions asked."})
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Post(s.url+"/botQuery", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response TextQueryResponse
	json.Unmarshal(data, &response)

	return ai.NewStreamFromString(response.Response), nil
}

func conversationToCompletion(conversation ai.BotConversation) string {
	result := ""
	for _, post := range conversation.Posts {
		if post.Role == ai.PostRoleBot {
			result += "<bot>: "
		} else {
			result += "<human>: "
		}
		result += post.Message
		result += "\n"
	}

	return result
}

func (s *MattermostAI) ContinueQuestionThread(conversation ai.BotConversation) (*ai.TextStreamResult, error) {
	prompt := conversationToCompletion(conversation)
	requestBody, err := json.Marshal(TextQueryRequest{Prompt: prompt, BotDescription: "You are a helpful assistant."})
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Post(s.url+"/botQuery", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response TextQueryResponse
	json.Unmarshal(data, &response)

	return ai.NewStreamFromString(response.Response), nil
}
