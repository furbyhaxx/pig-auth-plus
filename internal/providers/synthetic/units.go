package synthetic

import (
	"math"
	"unicode/utf8"
)

// streamFactors is streamed prompt usage divided by real tokens, per tokenizer repository
// (ANA-synthetic-usage-units, measured 2026-11-08). Re-measure when Synthetic adds a model.
var streamFactors = map[string]float64{
	"deepseek-ai/DeepSeek-V4.1-Flash-1017": 0.4212,
	"moonshotai/Kimi-K3":                   0.4210,
	"moonshotai/Kimi-K3.1-Pro":             0.4207,
	"zai-org/GLM-5.3":                      0.4210,
	"zai-org/GLM-5.3-Flash":                0.4210,
	"zai-org/GLM-4.7-Flash":                1.3560,
	"openai/gpt-oss-120b":                  2.4430,
	"Qwen/Qwen3.8-27B-FP8":                 0.9392,
}

// charsPerToken estimates an unmeasured model from its request body, which mixes prose,
// JSON tool schemas and JSON framing.
const charsPerToken = 3.5

// UnitCounter converts Synthetic's streamed prompt units to real tokens: a measured factor for
// known tokenizers, otherwise a character estimate of the request body.
func UnitCounter(c *Catalog) PromptCounter {
	return func(model string, reported int, payload []byte) (int, bool) {
		if f, ok := streamFactors[c.Repo(model)]; ok {
			return int(math.Round(float64(reported) / f)), true
		}
		if len(payload) == 0 {
			return 0, false
		}
		return int(math.Ceil(float64(utf8.RuneCount(payload)) / charsPerToken)), true
	}
}
