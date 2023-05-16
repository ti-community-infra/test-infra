package main

import (
	"fmt"
	"strings"

	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"
)

// Ref: https://platform.openai.com/docs/models
var maxTokens = map[string]int{
	openai.CodexCodeDavinci002: 8001,
	openai.GPT3Dot5Turbo:       4096,
	openai.GPT3Dot5Turbo0301:   4096,
	openai.GPT3TextDavinci002:  4097,
	openai.GPT3TextDavinci003:  4097,
	openai.GPT4:                8192,
	openai.GPT40314:            8192,
	openai.GPT432K:             32768,
	openai.GPT432K0314:         32768,
}

func splitUserMessage(messageText string, model string) []string {
	splitLen := maxTokens[model] - splitorHoldingByteCount
	if splitLen < 0 {
		return nil
	}

	if len(messageText) <= splitLen {
		return []string{messageText}
	}

	partCount := len(messageText) / splitLen
	if partCount*splitLen < len(messageText) {
		partCount += 1
	}

	var messages []string
	for i := 0; i < partCount; i++ {
		var chunkMessageLines []string
		isLast := i == partCount-1

		partFlag := fmt.Sprintf("PART %d/%d", i+1, partCount)
		startPos := splitLen * i
		endPos := startPos + splitLen
		if isLast {
			endPos = len(messageText)
		}

		if !isLast {
			chunkMessageLines = append(chunkMessageLines,
				fmt.Sprintf(`Do not answer yet. This is just another part of the text I want to send you. Just receive and acknowledge as "%s received" and wait for the next part.`, partFlag))
		}
		chunkMessageLines = append(chunkMessageLines,
			fmt.Sprintf("[START %s]", partFlag),
			messageText[startPos:endPos],
			fmt.Sprintf("[END %s]", partFlag),
		)
		if isLast {
			chunkMessageLines = append(chunkMessageLines, "ALL PARTS SENT. Now you can continue processing the request.")
		}

		messages = append(messages, strings.Join(chunkMessageLines, "\n"))
	}

	return messages
}

// ref: https://github.com/pkoukk/tiktoken-go#counting-tokens-for-chat-api-calls
func numTokensFromMessages(messages []openai.ChatCompletionMessage, model string) (int, error) {
	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		return 0, fmt.Errorf("EncodingForModel: %v", err)
	}

	var tokens_per_message int
	var tokens_per_name int
	switch model {
	case openai.GPT3Dot5Turbo, openai.GPT3Dot5Turbo0301:
		tokens_per_message = 4
		tokens_per_name = -1
	case openai.GPT4, openai.GPT40314, openai.GPT432K, openai.GPT432K0314:
		tokens_per_message = 3
		tokens_per_name = 1
	default:
		// model not found. Using cl100k_base encoding.
		tokens_per_message = 3
		tokens_per_name = 1
	}

	num_tokens := 0
	for _, message := range messages {
		num_tokens += tokens_per_message
		num_tokens += len(tkm.Encode(message.Content, nil, nil))
		num_tokens += len(tkm.Encode(message.Role, nil, nil))
		num_tokens += len(tkm.Encode(message.Name, nil, nil))
		if message.Name != "" {
			num_tokens += tokens_per_name
		}
	}
	num_tokens += 3

	return num_tokens, nil
}
