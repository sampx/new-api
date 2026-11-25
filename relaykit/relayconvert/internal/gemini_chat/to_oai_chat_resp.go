package geminichat

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	sharedgemini "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/gemini"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type GeminiOutputSummary struct {
	HasCandidate     bool
	HasVisibleOutput bool
	HasThoughtOutput bool
	HasPromptBlock   bool
	FinishReason     string
}

func IsAbnormalZeroOutputFinish(reason string) bool {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "MALFORMED_RESPONSE":
		return true
	default:
		return false
	}
}

func hasValidFunctionCallPart(fc *dto.FunctionCall) bool {
	if fc == nil {
		return false
	}
	if strings.TrimSpace(fc.FunctionName) != "" || strings.TrimSpace(fc.ID) != "" || len(fc.PartialArgs) > 0 {
		return true
	}
	switch v := fc.Arguments.(type) {
	case map[string]any:
		return len(v) > 0
	case string:
		return strings.TrimSpace(v) != ""
	default:
		return v != nil
	}
}

func InspectGeminiOutput(response *dto.GeminiChatResponse) GeminiOutputSummary {
	var summary GeminiOutputSummary
	if response == nil {
		return summary
	}
	if response.PromptFeedback != nil && response.PromptFeedback.BlockReason != nil && strings.TrimSpace(*response.PromptFeedback.BlockReason) != "" {
		summary.HasPromptBlock = true
	}
	if len(response.Candidates) > 0 {
		summary.HasCandidate = true
	}
	for _, candidate := range response.Candidates {
		if candidate.FinishReason != nil {
			if fr := strings.TrimSpace(*candidate.FinishReason); fr != "" && summary.FinishReason == "" {
				summary.FinishReason = fr
			}
		}
		if candidate.GroundingMetadata != nil && len(candidate.GroundingMetadata.WebSearchQueries) > 0 {
			summary.HasVisibleOutput = true
		}
		for _, part := range candidate.Content.Parts {
			hasMediaOrTool := hasValidFunctionCallPart(part.FunctionCall) ||
				(part.InlineData != nil && strings.TrimSpace(part.InlineData.Data) != "") ||
				(part.FileData != nil && strings.TrimSpace(part.FileData.FileUri) != "") ||
				(part.ExecutableCode != nil && strings.TrimSpace(part.ExecutableCode.Code) != "") ||
				(part.CodeExecutionResult != nil && (strings.TrimSpace(part.CodeExecutionResult.Output) != "" || strings.TrimSpace(part.CodeExecutionResult.Outcome) != ""))
			if part.Thought {
				if part.Text != "" || hasMediaOrTool {
					summary.HasThoughtOutput = true
				}
				continue
			}
			if hasMediaOrTool || strings.TrimSpace(part.Text) != "" {
				summary.HasVisibleOutput = true
			}
		}
	}
	return summary
}

func UsageFromGeminiMetadata(metadata *dto.GeminiUsageMetadata, fallbackPromptTokens int) *dto.Usage {
	if metadata == nil {
		if fallbackPromptTokens <= 0 {
			return nil
		}
		usage := &dto.Usage{PromptTokens: fallbackPromptTokens}
		usage.PromptTokensDetails.TextTokens = fallbackPromptTokens
		return usage
	}

	promptTokens := metadata.PromptTokenCount + metadata.ToolUsePromptTokenCount
	if promptTokens <= 0 && fallbackPromptTokens > 0 {
		promptTokens = fallbackPromptTokens
	}

	usage := &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount,
		TotalTokens:      metadata.TotalTokenCount,
		BillingUsage:     dto.CloneBillingUsage(metadata.BillingUsage),
	}
	if usage.BillingUsage == nil {
		usage.BillingUsage = dto.NewGeminiChatBillingUsage(metadata)
	}
	usage.CompletionTokenDetails.ReasoningTokens = metadata.ThoughtsTokenCount
	usage.PromptTokensDetails.CachedTokens = metadata.CachedContentTokenCount

	for _, detail := range metadata.PromptTokensDetails {
		if detail.Modality == "AUDIO" {
			usage.PromptTokensDetails.AudioTokens += detail.TokenCount
		} else if detail.Modality == "IMAGE" {
			usage.PromptTokensDetails.ImageTokens += detail.TokenCount
		} else if detail.Modality == "TEXT" {
			usage.PromptTokensDetails.TextTokens += detail.TokenCount
		}
	}
	for _, detail := range metadata.ToolUsePromptTokensDetails {
		if detail.Modality == "AUDIO" {
			usage.PromptTokensDetails.AudioTokens += detail.TokenCount
		} else if detail.Modality == "IMAGE" {
			usage.PromptTokensDetails.ImageTokens += detail.TokenCount
		} else if detail.Modality == "TEXT" {
			usage.PromptTokensDetails.TextTokens += detail.TokenCount
		}
	}
	for _, detail := range metadata.CandidatesTokensDetails {
		switch detail.Modality {
		case "IMAGE":
			usage.CompletionTokenDetails.ImageTokens += detail.TokenCount
		case "AUDIO":
			usage.CompletionTokenDetails.AudioTokens += detail.TokenCount
		case "TEXT":
			usage.CompletionTokenDetails.TextTokens += detail.TokenCount
		}
	}

	if usage.TotalTokens > 0 && usage.CompletionTokens <= 0 {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
	}

	if usage.PromptTokens > 0 && usage.PromptTokensDetails.TextTokens == 0 && usage.PromptTokensDetails.AudioTokens == 0 {
		usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	}

	return usage
}

func ResponseGeminiChat2OpenAI(id string, created int64, response *dto.GeminiChatResponse) *dto.OpenAITextResponse {
	fullTextResponse := dto.OpenAITextResponse{
		Id:      id,
		Object:  "chat.completion",
		Created: created,
		Choices: make([]dto.OpenAITextResponseChoice, 0, len(response.Candidates)),
	}
	isToolCall := false
	for _, candidate := range response.Candidates {
		choice := dto.OpenAITextResponseChoice{
			Index: int(candidate.Index),
			Message: dto.Message{
				Role:    "assistant",
				Content: "",
			},
			FinishReason: types.FinishReasonStop,
		}
		if len(candidate.Content.Parts) > 0 {
			var content strings.Builder
			var reasoningContent strings.Builder
			hasThought := false
			var inlineGrow int
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					inlineGrow += len(part.InlineData.MimeType) + len(part.InlineData.Data) + 32
				}
			}
			if inlineGrow > 0 {
				content.Grow(inlineGrow)
			}
			appended := 0
			lastWasText := false
			writeSep := func(text bool) {
				if appended > 0 && !(text && lastWasText) {
					content.WriteByte('\n')
				}
				appended++
				lastWasText = text
			}
			var toolCalls []dto.ToolCallResponse
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					if strings.HasPrefix(part.InlineData.MimeType, "image") {
						writeSep(false)
						content.WriteString("![image](data:")
						content.WriteString(part.InlineData.MimeType)
						content.WriteString(";base64,")
						content.WriteString(part.InlineData.Data)
						content.WriteByte(')')
					} else {
						writeSep(false)
						content.WriteString("[media](data:")
						content.WriteString(part.InlineData.MimeType)
						content.WriteString(";base64,")
						content.WriteString(part.InlineData.Data)
						content.WriteByte(')')
					}
				} else if part.FunctionCall != nil {
					choice.FinishReason = types.FinishReasonToolCalls
					if call := geminiResponseToolCall(&part); call != nil {
						toolCalls = append(toolCalls, *call)
					}
				} else if part.Thought {
					if part.Text != "" {
						hasThought = true
						reasoningContent.WriteString(part.Text)
					}
				} else {
					if part.ExecutableCode != nil {
						writeSep(false)
						content.WriteString("```")
						content.WriteString(part.ExecutableCode.Language)
						content.WriteByte('\n')
						content.WriteString(part.ExecutableCode.Code)
						content.WriteString("\n```")
					} else if part.CodeExecutionResult != nil {
						writeSep(false)
						content.WriteString("```output\n")
						content.WriteString(part.CodeExecutionResult.Output)
						content.WriteString("\n```")
					} else if part.Text != "" {
						writeSep(true)
						content.WriteString(part.Text)
					}
				}
			}
			if hasThought {
				reasoningStr := reasoningContent.String()
				choice.Message.ReasoningContent = &reasoningStr
			}
			if len(toolCalls) > 0 {
				choice.Message.SetToolCalls(toolCalls)
				isToolCall = true
			}
			choice.Message.SetStringContent(content.String())
		}
		if candidate.FinishReason != nil {
			switch *candidate.FinishReason {
			case "STOP":
				choice.FinishReason = types.FinishReasonStop
			case "MAX_TOKENS", "CONTINUATION":
				choice.FinishReason = types.FinishReasonLength
			case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "OTHER":
				choice.FinishReason = types.FinishReasonContentFilter
			default:
				choice.FinishReason = types.FinishReasonContentFilter
			}
		}
		if isToolCall {
			choice.FinishReason = types.FinishReasonToolCalls
		}

		fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	}
	return &fullTextResponse
}

func StreamResponseGeminiChat2OpenAI(geminiResponse *dto.GeminiChatResponse) (*dto.ChatCompletionsStreamResponse, bool) {
	choices := make([]dto.ChatCompletionsStreamResponseChoice, 0, len(geminiResponse.Candidates))
	isStop := false
	for _, candidate := range geminiResponse.Candidates {
		if candidate.FinishReason != nil && *candidate.FinishReason == "STOP" {
			isStop = true
			candidate.FinishReason = nil
		}
		choice := dto.ChatCompletionsStreamResponseChoice{
			Index: int(candidate.Index),
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{},
		}
		var content strings.Builder
		var reasoning strings.Builder
		var inlineGrow int
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				inlineGrow += len(part.InlineData.MimeType) + len(part.InlineData.Data) + 32
			}
		}
		if inlineGrow > 0 {
			content.Grow(inlineGrow)
		}
		appended := 0
		lastWasText := false
		writeSep := func(text bool) {
			if appended > 0 && !(text && lastWasText) {
				content.WriteByte('\n')
			}
			appended++
			lastWasText = text
		}
		isTools := false
		if candidate.FinishReason != nil {
			switch *candidate.FinishReason {
			case "STOP":
				choice.FinishReason = &types.FinishReasonStop
			case "MAX_TOKENS", "CONTINUATION":
				choice.FinishReason = &types.FinishReasonLength
			case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "OTHER":
				choice.FinishReason = &types.FinishReasonContentFilter
			default:
				choice.FinishReason = &types.FinishReasonContentFilter
			}
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				if strings.HasPrefix(part.InlineData.MimeType, "image") {
					writeSep(false)
					content.WriteString("![image](data:")
					content.WriteString(part.InlineData.MimeType)
					content.WriteString(";base64,")
					content.WriteString(part.InlineData.Data)
					content.WriteByte(')')
				}
			} else if part.FunctionCall != nil {
				isTools = true
				if call := geminiResponseToolCall(&part); call != nil {
					call.SetIndex(len(choice.Delta.ToolCalls))
					choice.Delta.ToolCalls = append(choice.Delta.ToolCalls, *call)
				}
			} else if part.Thought {
				if part.Text != "" {
					reasoning.WriteString(part.Text)
				}
			} else {
				if part.ExecutableCode != nil {
					writeSep(false)
					content.WriteString("```")
					content.WriteString(part.ExecutableCode.Language)
					content.WriteByte('\n')
					content.WriteString(part.ExecutableCode.Code)
					content.WriteString("\n```\n")
				} else if part.CodeExecutionResult != nil {
					writeSep(false)
					content.WriteString("```output\n")
					content.WriteString(part.CodeExecutionResult.Output)
					content.WriteString("\n```\n")
				} else if part.Text != "" {
					writeSep(true)
					content.WriteString(part.Text)
				}
			}
		}
		if reasoning.Len() > 0 {
			choice.Delta.SetReasoningContent(reasoning.String())
		}
		if content.Len() > 0 {
			choice.Delta.SetContentString(content.String())
		}
		if isTools {
			choice.FinishReason = &types.FinishReasonToolCalls
		}
		choices = append(choices, choice)
	}

	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Choices: choices,
	}
	return &response, isStop
}

type GeminiToChatStreamState struct {
	id                       string
	created                  int64
	sawToolCall              bool
	sawVisibleOutput         bool
	sawThoughtOutput         bool
	upstreamFinishReason     string
	finishEmitted            bool
	latestUsage              *dto.Usage
	nextToolIndexByCandidate map[int64]int
	toolIndexByCandidateID   map[int64]map[string]int
	partialToolByCandidate   map[int64]*geminiPartialToolCall
}

type geminiPartialToolCall struct {
	id        string
	name      string
	signature string
	arguments map[string]any
}

type geminiPartialArgPathSegment struct {
	member  string
	index   int
	isIndex bool
}

const maxGeminiPartialArgArrayIndex = 4095

func NewGeminiToChatStreamState(id string, created int64) *GeminiToChatStreamState {
	id = strings.TrimSpace(id)
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%s", kitutil.GetUUID())
	}
	if created == 0 {
		created = kitutil.GetTimestamp()
	}
	return &GeminiToChatStreamState{
		id:                       id,
		created:                  created,
		nextToolIndexByCandidate: make(map[int64]int),
		toolIndexByCandidateID:   make(map[int64]map[string]int),
		partialToolByCandidate:   make(map[int64]*geminiPartialToolCall),
	}
}

func (s *GeminiToChatStreamState) ensureMaps() {
	if s.nextToolIndexByCandidate == nil {
		s.nextToolIndexByCandidate = make(map[int64]int)
	}
	if s.toolIndexByCandidateID == nil {
		s.toolIndexByCandidateID = make(map[int64]map[string]int)
	}
	if s.partialToolByCandidate == nil {
		s.partialToolByCandidate = make(map[int64]*geminiPartialToolCall)
	}
}

func (s *GeminiToChatStreamState) HasVisibleOutput() bool {
	return s != nil && s.sawVisibleOutput
}

func (s *GeminiToChatStreamState) HasThoughtOutput() bool {
	return s != nil && s.sawThoughtOutput
}

func (s *GeminiToChatStreamState) ConvertChunk(geminiResponse *dto.GeminiChatResponse, model string, usage *dto.Usage) ([]*dto.ChatCompletionsStreamResponse, error) {
	if s == nil || geminiResponse == nil {
		return nil, nil
	}
	s.ensureMaps()

	var err error
	geminiResponse, err = s.preparePartialFunctionCalls(geminiResponse)
	if err != nil {
		return nil, err
	}

	summary := InspectGeminiOutput(geminiResponse)
	if summary.HasVisibleOutput {
		s.sawVisibleOutput = true
	}
	if summary.HasThoughtOutput {
		s.sawThoughtOutput = true
	}
	if summary.FinishReason != "" {
		s.upstreamFinishReason = summary.FinishReason
	}
	if IsAbnormalZeroOutputFinish(s.upstreamFinishReason) && !s.sawVisibleOutput {
		return nil, fmt.Errorf("Gemini stream terminated with %s and zero visible output", s.upstreamFinishReason)
	}

	hasNonStopFinish := false
	for _, candidate := range geminiResponse.Candidates {
		if candidate.FinishReason != nil && *candidate.FinishReason != "" && *candidate.FinishReason != "STOP" {
			hasNonStopFinish = true
			break
		}
	}
	response, isStop := StreamResponseGeminiChat2OpenAI(geminiResponse)
	if response == nil {
		return nil, nil
	}
	response.Id = s.id
	response.Created = s.created
	response.Model = model
	response.Usage = usage

	for index := range geminiResponse.Candidates {
		if index >= len(response.Choices) {
			break
		}
		candidate := &geminiResponse.Candidates[index]
		choice := &response.Choices[index]
		for toolIndex := range choice.Delta.ToolCalls {
			callID := strings.TrimSpace(choice.Delta.ToolCalls[toolIndex].ID)
			indexesByID := s.toolIndexByCandidateID[candidate.Index]
			if indexesByID == nil {
				indexesByID = make(map[string]int)
				s.toolIndexByCandidateID[candidate.Index] = indexesByID
			}
			stableIndex, exists := indexesByID[callID]
			if callID == "" || !exists {
				stableIndex = s.nextToolIndexByCandidate[candidate.Index]
				s.nextToolIndexByCandidate[candidate.Index] = stableIndex + 1
				if callID != "" {
					indexesByID[callID] = stableIndex
				}
			}
			choice.Delta.ToolCalls[toolIndex].SetIndex(stableIndex)
		}
	}

	if response.IsToolCall() {
		s.sawToolCall = true
		s.sawVisibleOutput = true
		if !hasNonStopFinish {
			for i := range response.Choices {
				if response.Choices[i].FinishReason != nil && *response.Choices[i].FinishReason == types.FinishReasonToolCalls {
					response.Choices[i].FinishReason = nil
				}
			}
		}
	}
	if usage != nil {
		s.latestUsage = usage
	}

	hasDeltaOrFinish := false
	for _, choice := range response.Choices {
		if choice.Delta.Content != nil || choice.Delta.ReasoningContent != nil || len(choice.Delta.ToolCalls) > 0 {
			hasDeltaOrFinish = true
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finishEmitted = true
			hasDeltaOrFinish = true
		}
	}

	var responses []*dto.ChatCompletionsStreamResponse
	if hasDeltaOrFinish || (len(response.Choices) == 0 && usage != nil && (s.sawVisibleOutput || s.sawThoughtOutput)) {
		responses = append(responses, response)
	}
	if isStop && !s.finishEmitted && (s.sawVisibleOutput || s.sawThoughtOutput) {
		responses = append(responses, s.terminalChunk(model))
	}
	return responses, nil
}

func (s *GeminiToChatStreamState) Finalize(model string) ([]*dto.ChatCompletionsStreamResponse, error) {
	if s == nil {
		return nil, nil
	}
	if len(s.partialToolByCandidate) > 0 {
		candidateIndexes := make([]int64, 0, len(s.partialToolByCandidate))
		for candidateIndex := range s.partialToolByCandidate {
			candidateIndexes = append(candidateIndexes, candidateIndex)
		}
		sort.Slice(candidateIndexes, func(i, j int) bool {
			return candidateIndexes[i] < candidateIndexes[j]
		})
		candidateIndex := candidateIndexes[0]
		partial := s.partialToolByCandidate[candidateIndex]
		return nil, fmt.Errorf("Gemini stream ended with an incomplete function call for candidate %d (id %q, name %q)", candidateIndex, partial.id, partial.name)
	}
	if IsAbnormalZeroOutputFinish(s.upstreamFinishReason) && !s.sawVisibleOutput {
		return nil, fmt.Errorf("Gemini stream terminated with %s and zero visible output", s.upstreamFinishReason)
	}
	if !s.sawVisibleOutput && !s.sawThoughtOutput {
		return nil, fmt.Errorf("Gemini stream ended with zero output")
	}
	if s.finishEmitted {
		return nil, nil
	}
	return []*dto.ChatCompletionsStreamResponse{s.terminalChunk(model)}, nil
}

func (s *GeminiToChatStreamState) Usage() *dto.Usage {
	if s == nil {
		return nil
	}
	return s.latestUsage
}

func (s *GeminiToChatStreamState) preparePartialFunctionCalls(response *dto.GeminiChatResponse) (*dto.GeminiChatResponse, error) {
	prepared := *response
	prepared.Candidates = append([]dto.GeminiChatCandidate(nil), response.Candidates...)
	for candidateIndex := range prepared.Candidates {
		candidate := &prepared.Candidates[candidateIndex]
		parts := make([]dto.GeminiPart, 0, len(candidate.Content.Parts))
		for _, part := range candidate.Content.Parts {
			call := part.FunctionCall
			if call == nil || (s.partialToolByCandidate[candidate.Index] == nil && call.WillContinue == nil && len(call.PartialArgs) == 0) {
				parts = append(parts, part)
				continue
			}
			completed, sig, ready, err := s.appendPartialFunctionCall(candidate.Index, call, sharedgemini.ThoughtSignatureValue(part))
			if err != nil {
				return nil, fmt.Errorf("reconstruct Gemini streamed function arguments: %w", err)
			}
			if ready {
				part.FunctionCall = completed
				if sig != "" && len(part.ThoughtSignature) == 0 {
					sharedgemini.SetThoughtSignature(&part, sig)
				}
				parts = append(parts, part)
			}
		}
		candidate.Content.Parts = parts
	}
	return &prepared, nil
}

func (s *GeminiToChatStreamState) appendPartialFunctionCall(candidateIndex int64, call *dto.FunctionCall, partSig string) (*dto.FunctionCall, string, bool, error) {
	current := s.partialToolByCandidate[candidateIndex]
	if current == nil {
		current = &geminiPartialToolCall{arguments: make(map[string]any)}
		s.partialToolByCandidate[candidateIndex] = current
	}
	if partSig != "" && current.signature == "" {
		current.signature = partSig
	}
	if id := strings.TrimSpace(call.ID); id != "" {
		if current.id != "" && current.id != id {
			return nil, "", false, fmt.Errorf("candidate %d function call changed id from %q to %q", candidateIndex, current.id, id)
		}
		current.id = id
	}
	if name := strings.TrimSpace(call.FunctionName); name != "" {
		if current.name != "" && current.name != name {
			return nil, "", false, fmt.Errorf("candidate %d function call changed name from %q to %q", candidateIndex, current.name, name)
		}
		current.name = name
	}
	for _, partial := range call.PartialArgs {
		path, err := parseGeminiPartialArgPath(partial.JSONPath)
		if err != nil {
			return nil, "", false, err
		}
		value, present := geminiPartialArgValue(partial)
		if !present {
			continue
		}
		updated, err := setGeminiPartialArgValue(current.arguments, path, value, partial.StringValue != nil)
		if err != nil {
			return nil, "", false, fmt.Errorf("set partial argument %q: %w", partial.JSONPath, err)
		}
		arguments, ok := updated.(map[string]any)
		if !ok {
			return nil, "", false, fmt.Errorf("partial argument path %q replaced the arguments object", partial.JSONPath)
		}
		current.arguments = arguments
	}
	if call.WillContinue != nil && *call.WillContinue {
		return nil, "", false, nil
	}
	if current.name == "" {
		return nil, "", false, fmt.Errorf("candidate %d completed a partial function call without a name", candidateIndex)
	}
	completed := &dto.FunctionCall{ID: current.id, FunctionName: current.name, Arguments: current.arguments}
	sig := current.signature
	delete(s.partialToolByCandidate, candidateIndex)
	return completed, sig, true, nil
}

func parseGeminiPartialArgPath(jsonPath string) ([]geminiPartialArgPathSegment, error) {
	path := strings.TrimSpace(jsonPath)
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("unsupported Gemini partial argument path %q", jsonPath)
	}
	segments := make([]geminiPartialArgPathSegment, 0)
	for offset := 1; offset < len(path); {
		switch path[offset] {
		case '.':
			offset++
			start := offset
			for offset < len(path) && path[offset] != '.' && path[offset] != '[' {
				offset++
			}
			if start == offset {
				return nil, fmt.Errorf("empty member in Gemini partial argument path %q", jsonPath)
			}
			member := path[start:offset]
			if strings.ContainsAny(member, "]*?") {
				return nil, fmt.Errorf("unsupported member %q in Gemini partial argument path", member)
			}
			segments = append(segments, geminiPartialArgPathSegment{member: member})
		case '[':
			offset++
			if offset >= len(path) {
				return nil, fmt.Errorf("unterminated selector in Gemini partial argument path %q", jsonPath)
			}
			if path[offset] == '\'' || path[offset] == '"' {
				member, next, err := parseGeminiPartialArgMember(path, offset)
				if err != nil {
					return nil, fmt.Errorf("invalid Gemini partial argument path %q: %w", jsonPath, err)
				}
				offset = next
				if offset >= len(path) || path[offset] != ']' {
					return nil, fmt.Errorf("unterminated member selector in Gemini partial argument path %q", jsonPath)
				}
				offset++
				segments = append(segments, geminiPartialArgPathSegment{member: member})
				continue
			}
			start := offset
			for offset < len(path) && path[offset] >= '0' && path[offset] <= '9' {
				offset++
			}
			if start == offset || offset >= len(path) || path[offset] != ']' {
				return nil, fmt.Errorf("unsupported selector in Gemini partial argument path %q", jsonPath)
			}
			index, err := strconv.Atoi(path[start:offset])
			if err != nil {
				return nil, fmt.Errorf("invalid array index in Gemini partial argument path %q: %w", jsonPath, err)
			}
			if index > maxGeminiPartialArgArrayIndex {
				return nil, fmt.Errorf("array index %d exceeds Gemini partial argument materialization limit %d", index, maxGeminiPartialArgArrayIndex)
			}
			offset++
			segments = append(segments, geminiPartialArgPathSegment{index: index, isIndex: true})
		default:
			return nil, fmt.Errorf("unsupported selector at offset %d in Gemini partial argument path %q", offset, jsonPath)
		}
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("Gemini partial argument path %q targets the arguments root", jsonPath)
	}
	return segments, nil
}

func geminiPartialArgValue(partial dto.GeminiPartialArg) (any, bool) {
	switch {
	case partial.StringValue != nil:
		return *partial.StringValue, true
	case partial.NumberValue != nil:
		return *partial.NumberValue, true
	case partial.BoolValue != nil:
		return *partial.BoolValue, true
	case partial.NullValue != nil:
		return nil, true
	default:
		return nil, false
	}
}

func parseGeminiPartialArgMember(path string, offset int) (string, int, error) {
	quote := path[offset]
	start := offset
	offset++
	for offset < len(path) {
		if path[offset] == '\\' {
			offset += 2
			continue
		}
		if path[offset] == quote {
			raw := path[start : offset+1]
			if quote == '\'' {
				raw = "\"" + strings.ReplaceAll(strings.ReplaceAll(raw[1:len(raw)-1], "\"", "\\\""), "\\'", "'") + "\""
			}
			var member string
			if err := kitutil.Unmarshal([]byte(raw), &member); err != nil {
				return "", 0, err
			}
			return member, offset + 1, nil
		}
		offset++
	}
	return "", 0, fmt.Errorf("unterminated quoted member")
}

func setGeminiPartialArgValue(current any, path []geminiPartialArgPathSegment, value any, appendString bool) (any, error) {
	if len(path) == 0 {
		if appendString {
			if existing, ok := current.(string); ok {
				return existing + value.(string), nil
			}
		}
		return value, nil
	}
	segment := path[0]
	if segment.isIndex {
		var array []any
		switch typed := current.(type) {
		case nil:
			array = make([]any, segment.index+1)
		case []any:
			array = typed
			if len(array) <= segment.index {
				array = append(array, make([]any, segment.index-len(array)+1)...)
			}
		default:
			return nil, fmt.Errorf("array index %d traverses %T", segment.index, current)
		}
		updated, err := setGeminiPartialArgValue(array[segment.index], path[1:], value, appendString)
		if err != nil {
			return nil, err
		}
		array[segment.index] = updated
		return array, nil
	}

	var object map[string]any
	switch typed := current.(type) {
	case nil:
		object = make(map[string]any)
	case map[string]any:
		object = typed
	default:
		return nil, fmt.Errorf("member %q traverses %T", segment.member, current)
	}
	updated, err := setGeminiPartialArgValue(object[segment.member], path[1:], value, appendString)
	if err != nil {
		return nil, err
	}
	object[segment.member] = updated
	return object, nil
}

func (s *GeminiToChatStreamState) terminalChunk(model string) *dto.ChatCompletionsStreamResponse {
	finishReason := types.FinishReasonStop
	if s.sawToolCall {
		finishReason = types.FinishReasonToolCalls
	}
	s.finishEmitted = true
	return &dto.ChatCompletionsStreamResponse{
		Id:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
				FinishReason: &finishReason,
			},
		},
		Usage: s.latestUsage,
	}
}

func geminiResponseToolCall(item *dto.GeminiPart) *dto.ToolCallResponse {
	argsBytes, err := kitutil.Marshal(item.FunctionCall.Arguments)
	if err != nil {
		return nil
	}
	callID := strings.TrimSpace(item.FunctionCall.ID)
	if callID == "" {
		callID = fmt.Sprintf("call_%s", kitutil.GetUUID())
	}
	resp := &dto.ToolCallResponse{
		ID:   callID,
		Type: "function",
		Function: dto.FunctionResponse{
			Arguments: string(argsBytes),
			Name:      item.FunctionCall.FunctionName,
		},
	}
	if sig := sharedgemini.ThoughtSignatureValue(*item); sig != "" && !sharedgemini.IsThoughtSignatureBypass(sig) {
		resp.ExtraContent = &dto.ToolCallExtraContent{
			Google: &dto.ToolCallGoogleExtraContent{
				ThoughtSignature: sig,
			},
		}
	}
	return resp
}
