package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"strings"

	"github.com/crspeller/mattermost-plugin-summarize/server/ai"
	"github.com/sashabaranov/go-openai"
	openaiClient "github.com/sashabaranov/go-openai"
)

type OpenAI struct {
	client *openaiClient.Client
}

func New(apiKey string) *OpenAI {
	return &OpenAI{
		client: openaiClient.NewClient(apiKey),
	}
}

func conversationToCompletion(conversation ai.BotConversation) []openaiClient.ChatCompletionMessage {
	result := make([]openaiClient.ChatCompletionMessage, 0, len(conversation.Posts))

	for _, post := range conversation.Posts {
		role := openaiClient.ChatMessageRoleUser
		if post.Role == ai.PostRoleBot {
			role = openaiClient.ChatMessageRoleAssistant
		}
		result = append(result, openai.ChatCompletionMessage{
			Role:    role,
			Content: post.Message,
		})
	}

	return result
}

func (s *OpenAI) ThreadCompletion(systemMessage string, conversation ai.BotConversation) (*ai.TextStreamResult, error) {
	request := openaiClient.ChatCompletionRequest{
		Model: openaiClient.GPT3Dot5Turbo,
		Messages: append(
			[]openaiClient.ChatCompletionMessage{{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemMessage,
			}},
			conversationToCompletion(conversation)...,
		),
		Stream: true,
	}

	return s.streamResult(request)
}

func (s *OpenAI) ContinueQuestionThread(posts ai.BotConversation) (*ai.TextStreamResult, error) {
	return s.ThreadCompletion(GenericQuestionSystemMessage, posts)
}

func (s *OpenAI) streamResult(request openaiClient.ChatCompletionRequest) (*ai.TextStreamResult, error) {
	output := make(chan string)
	go func() {
		request.Stream = true
		stream, err := s.client.CreateChatCompletionStream(context.Background(), request)
		if err != nil {
			fmt.Println("Stream error: " + err.Error())
			return
		}

		defer stream.Close()
		defer close(output)

		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}

			if err != nil {
				fmt.Println("Stream error: " + err.Error())
				return
			}

			output <- response.Choices[0].Delta.Content
		}
	}()

	return &ai.TextStreamResult{Stream: output}, nil
}

func (s *OpenAI) SummarizeThread(thread string) (*ai.TextStreamResult, error) {
	request := openaiClient.ChatCompletionRequest{
		Model: openaiClient.GPT3Dot5Turbo,
		Messages: []openaiClient.ChatCompletionMessage{
			{
				Role:    openaiClient.ChatMessageRoleSystem,
				Content: SummarizeThreadSystemMessage,
			},
			{
				Role:    openaiClient.ChatMessageRoleUser,
				Content: thread,
			},
		},
		Stream: true,
	}
	return s.streamResult(request)
}

func (s *OpenAI) ContinueThreadInterrogation(thread string, posts ai.BotConversation) (*ai.TextStreamResult, error) {
	reqeust := openaiClient.ChatCompletionRequest{
		Model: openaiClient.GPT3Dot5Turbo,
		Messages: append(
			[]openaiClient.ChatCompletionMessage{
				{
					Role:    openaiClient.ChatMessageRoleSystem,
					Content: AnswerThreadQuestionSystemMessage,
				},
				{
					Role:    openaiClient.ChatMessageRoleUser,
					Content: thread,
				},
			},
			conversationToCompletion(posts)...,
		),
		Stream: true,
	}

	return s.streamResult(reqeust)
}

func (s *OpenAI) GenerateImage(prompt string) (image.Image, error) {
	req := openaiClient.ImageRequest{
		Prompt:         prompt,
		Size:           openai.CreateImageSize256x256,
		ResponseFormat: openai.CreateImageResponseFormatB64JSON,
		N:              1,
	}

	respBase64, err := s.client.CreateImage(context.Background(), req)
	if err != nil {
		return nil, err
	}

	imgBytes, err := base64.StdEncoding.DecodeString(respBase64.Data[0].B64JSON)
	if err != nil {
		return nil, err
	}

	r := bytes.NewReader(imgBytes)
	imgData, err := png.Decode(r)
	if err != nil {
		return nil, err
	}

	return imgData, nil
}

func (s *OpenAI) SelectEmoji(message string) (string, error) {
	resp, err := s.client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:     openai.GPT3Dot5Turbo,
			MaxTokens: 25,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: EmojiSystemMessage,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: message,
				},
			},
		},
	)
	if err != nil {
		return "", err
	}
	result := strings.Trim(strings.TrimSpace(resp.Choices[0].Message.Content), ":")

	return result, nil
}
