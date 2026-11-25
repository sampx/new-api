package gemini

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
)

var SupportedMimeTypes = map[string]bool{
	"application/pdf": true,
	"audio/mpeg":      true,
	"audio/mp3":       true,
	"audio/wav":       true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/jpg":       true,
	"image/webp":      true,
	"image/heic":      true,
	"image/heif":      true,
	"text/plain":      true,
	"video/mov":       true,
	"video/mpeg":      true,
	"video/mp4":       true,
	"video/mpg":       true,
	"video/avi":       true,
	"video/wmv":       true,
	"video/mpegps":    true,
	"video/flv":       true,
}

var SafetySettingCategories = []string{
	"HARM_CATEGORY_HARASSMENT",
	"HARM_CATEGORY_HATE_SPEECH",
	"HARM_CATEGORY_SEXUALLY_EXPLICIT",
	"HARM_CATEGORY_DANGEROUS_CONTENT",
}

const ThoughtSignatureBypassValue = "context_engineering_is_the_way_to_go"

const (
	pro25MinBudget       = 128
	pro25MaxBudget       = 32768
	flash25MaxBudget     = 24576
	flash25LiteMinBudget = 512
	flash25LiteMaxBudget = 24576
)

func ShouldAttachThoughtSignature(opts *convmeta.Options) bool {
	return opts != nil && opts.Gemini.FunctionCallThoughtSignatureEnabled
}

func AttachThoughtSignatureBypass(opts *convmeta.Options, part *dto.GeminiPart) bool {
	if part == nil || len(part.ThoughtSignature) > 0 || !ShouldAttachThoughtSignature(opts) {
		return false
	}
	part.ThoughtSignature = []byte(strconv.Quote(ThoughtSignatureBypassValue))
	return true
}

func AttachFunctionCallThoughtSignature(opts *convmeta.Options, part *dto.GeminiPart) bool {
	if part == nil || !HasFunctionCallContent(part.FunctionCall) {
		return false
	}
	return AttachThoughtSignatureBypass(opts, part)
}

func AttachFirstTextThoughtSignature(opts *convmeta.Options, parts []dto.GeminiPart) bool {
	if !ShouldAttachThoughtSignature(opts) {
		return false
	}
	for i := range parts {
		if parts[i].Text != "" && len(parts[i].ThoughtSignature) == 0 {
			parts[i].ThoughtSignature = []byte(strconv.Quote(ThoughtSignatureBypassValue))
			return true
		}
	}
	return false
}

func ApplyThinkingConfig(geminiRequest *dto.GeminiChatRequest, info convmeta.Meta, oaiRequest ...dto.GeneralOpenAIRequest) {
	opts := convmeta.OptionsOf(info)
	if geminiRequest == nil || info == nil {
		return
	}
	if !opts.Gemini.ThinkingAdapterEnabled {
		// 思考适配器只管 new-api 自己的模型名后缀约定（-thinking / -nothinking /
		// -high…）。标准 OpenAI reasoning_effort 是协议字段，必须始终生效，
		// 不能被这个私有开关挡住。
		applyReasoningEffortConfig(geminiRequest, info, oaiRequest...)
		return
	}

	modelName := convmeta.UpstreamModelName(info)
	isNew25Pro := strings.HasPrefix(modelName, "gemini-2.5-pro") &&
		!strings.HasPrefix(modelName, "gemini-2.5-pro-preview-05-06") &&
		!strings.HasPrefix(modelName, "gemini-2.5-pro-preview-03-25")

	if strings.Contains(modelName, "-thinking-") {
		parts := strings.SplitN(modelName, "-thinking-", 2)
		if len(parts) == 2 && parts[1] != "" {
			if budgetTokens, err := strconv.Atoi(parts[1]); err == nil {
				clampedBudget := clampThinkingBudget(modelName, budgetTokens)
				geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
					ThinkingBudget:  kitutil.GetPointer(clampedBudget),
					IncludeThoughts: true,
				}
			}
		}
	} else if strings.HasSuffix(modelName, "-thinking") {
		unsupportedModels := []string{
			"gemini-2.5-pro-preview-05-06",
			"gemini-2.5-pro-preview-03-25",
		}
		isUnsupported := false
		for _, unsupportedModel := range unsupportedModels {
			if strings.HasPrefix(modelName, unsupportedModel) {
				isUnsupported = true
				break
			}
		}

		if isUnsupported {
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
				IncludeThoughts: true,
			}
		} else {
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
				IncludeThoughts: true,
			}
			if geminiRequest.GenerationConfig.MaxOutputTokens != nil && *geminiRequest.GenerationConfig.MaxOutputTokens > 0 {
				budgetTokens := opts.Gemini.ThinkingAdapterBudgetTokensPercentage * float64(*geminiRequest.GenerationConfig.MaxOutputTokens)
				clampedBudget := clampThinkingBudget(modelName, int(budgetTokens))
				geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget = kitutil.GetPointer(clampedBudget)
			} else if len(oaiRequest) > 0 {
				geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget = kitutil.GetPointer(clampThinkingBudgetByEffort(modelName, oaiRequest[0].ReasoningEffort))
			}
		}
	} else if strings.HasSuffix(modelName, "-nothinking") {
		if !isNew25Pro {
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
				ThinkingBudget: kitutil.GetPointer(0),
			}
		}
	} else if _, level, ok := reasoning.TrimEffortSuffix(modelName); ok && level != "" {
		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   level,
		}
		info.SetReasoningEffort(level)
	} else {
		applyReasoningEffortConfig(geminiRequest, info, oaiRequest...)
	}
}

// geminiThinkingDialect 区分 Gemini 的两种思考控制方言：2.x 及更早用
// thinkingBudget，3.x 起改用 thinkingLevel。混用会被上游以 400 拒绝。
type geminiThinkingDialect int

const (
	geminiThinkingDialectUnknown geminiThinkingDialect = iota
	geminiThinkingDialectBudget
	geminiThinkingDialectLevel
)

func geminiThinkingDialectOf(modelName string) geminiThinkingDialect {
	version, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(modelName)), "gemini-")
	if !ok {
		return geminiThinkingDialectUnknown
	}
	version = strings.TrimPrefix(version, "v")
	// 取版本号前导数字：同时覆盖 "2.5-pro"、"3.8-flash"、"4-hydrogen" 三种写法。
	majorRaw := version
	if i := strings.IndexFunc(version, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		majorRaw = version[:i]
	}
	major, err := strconv.Atoi(majorRaw)
	if err != nil {
		return geminiThinkingDialectUnknown
	}
	if major >= 3 {
		return geminiThinkingDialectLevel
	}
	return geminiThinkingDialectBudget
}

// applyReasoningEffortConfig 把标准 OpenAI reasoning_effort 落成 Gemini
// thinkingConfig。调用方已写入的 thinkingConfig（extra_body.google.thinking_config
// 是 lossless 表示）优先，不覆盖；模型版本方言未知时不猜强度。
func applyReasoningEffortConfig(geminiRequest *dto.GeminiChatRequest, info convmeta.Meta, oaiRequest ...dto.GeneralOpenAIRequest) {
	if geminiRequest.GenerationConfig.ThinkingConfig != nil || len(oaiRequest) == 0 {
		return
	}
	effort := strings.ToLower(strings.TrimSpace(oaiRequest[0].ReasoningEffort))
	if effort == "" {
		return
	}
	modelName := convmeta.UpstreamModelName(info)
	switch geminiThinkingDialectOf(modelName) {
	case geminiThinkingDialectBudget:
		// thinkingBudget 方言：沿用本仓既有的档位→预算换算；none 表示关闭
		// 思考，clamp 会把不支持关闭的模型抬到各自的最小预算。
		budget := clampThinkingBudgetByEffort(modelName, effort)
		if effort == "none" {
			budget = clampThinkingBudget(modelName, 0)
		}
		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			ThinkingBudget: kitutil.GetPointer(budget),
		}
		info.SetReasoningEffort(effort)
	case geminiThinkingDialectLevel:
		// thinkingLevel 方言无法关闭思考，none 不下发强度，交由上游默认。
		if effort == "none" {
			return
		}
		// Gemini 官方枚举是 minimal/low/medium/high；xhigh 为 Gemini 4
		// Hydrogen 及以上支持；max 没有对应枚举，归到最高档 xhigh。
		if effort == "max" {
			effort = "xhigh"
		}
		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			ThinkingLevel: effort,
		}
		info.SetReasoningEffort(effort)
	}
}

func ParseStopSequences(stop any) []string {
	if stop == nil {
		return nil
	}

	switch v := stop.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []interface{}:
		sequences := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok && str != "" {
				sequences = append(sequences, str)
			}
		}
		return sequences
	}
	return nil
}

func HasFunctionCallContent(call *dto.FunctionCall) bool {
	if call == nil {
		return false
	}
	if strings.TrimSpace(call.FunctionName) != "" {
		return true
	}

	switch v := call.Arguments.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case map[string]interface{}:
		return len(v) > 0
	case []interface{}:
		return len(v) > 0
	default:
		return true
	}
}

func SupportedMimeTypesList() []string {
	keys := make([]string, 0, len(SupportedMimeTypes))
	for key := range SupportedMimeTypes {
		keys = append(keys, key)
	}
	return keys
}

func isNew25ProModel(modelName string) bool {
	return strings.HasPrefix(modelName, "gemini-2.5-pro") &&
		!strings.HasPrefix(modelName, "gemini-2.5-pro-preview-05-06") &&
		!strings.HasPrefix(modelName, "gemini-2.5-pro-preview-03-25")
}

func is25FlashLiteModel(modelName string) bool {
	return strings.HasPrefix(modelName, "gemini-2.5-flash-lite")
}

func clampThinkingBudget(modelName string, budget int) int {
	isNew25Pro := isNew25ProModel(modelName)
	is25FlashLite := is25FlashLiteModel(modelName)

	if is25FlashLite {
		if budget < flash25LiteMinBudget {
			return flash25LiteMinBudget
		}
		if budget > flash25LiteMaxBudget {
			return flash25LiteMaxBudget
		}
	} else if isNew25Pro {
		if budget < pro25MinBudget {
			return pro25MinBudget
		}
		if budget > pro25MaxBudget {
			return pro25MaxBudget
		}
	} else {
		if budget < 0 {
			return 0
		}
		if budget > flash25MaxBudget {
			return flash25MaxBudget
		}
	}
	return budget
}

func clampThinkingBudgetByEffort(modelName string, effort string) int {
	isNew25Pro := isNew25ProModel(modelName)
	is25FlashLite := is25FlashLiteModel(modelName)

	maxBudget := 0
	if is25FlashLite {
		maxBudget = flash25LiteMaxBudget
	}
	if isNew25Pro {
		maxBudget = pro25MaxBudget
	} else {
		maxBudget = flash25MaxBudget
	}
	switch effort {
	case "high":
		maxBudget = maxBudget * 80 / 100
	case "medium":
		maxBudget = maxBudget * 50 / 100
	case "low":
		maxBudget = maxBudget * 20 / 100
	case "minimal":
		maxBudget = maxBudget * 5 / 100
	}
	return clampThinkingBudget(modelName, maxBudget)
}
