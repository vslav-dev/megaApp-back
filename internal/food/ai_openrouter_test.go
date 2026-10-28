package food

import "testing"

func TestParseAIProductResponseAcceptsMarkdownJSON(t *testing.T) {
	result, err := parseAIProductResponse("```json\n{\n  \"generalizedName\": \"Говно на палочке\",\n  \"kcals\": 123,\n  \"protein\": 4.5,\n  \"fat\": 6.7,\n  \"carbs\": 8.9,\n  \"fiber\": 1.2,\n  \"description\": \"Тестовый продукт\"\n}\n```")
	if err != nil {
		t.Fatalf("parseAIProductResponse() error = %v", err)
	}
	if result.GeneralizedName != "Говно на палочке" || result.Kcals != 123 {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseAIProductResponseRejectsIncompleteData(t *testing.T) {
	_, err := parseAIProductResponse(`{"generalizedName":"Продукт"}`)
	if err == nil {
		t.Fatal("parseAIProductResponse() error = nil, want error")
	}
}

// Reproduces the qwen3.5-flash-02-23 prod incident: the model's reasoning trace has no opening
// <think> tag (swallowed by the provider) but a literal stray "{" / "}" pair, followed by the
// closing </think> and the real answer. The old first-'{'-to-last-'}' regex spanned from the stray
// brace to the real closing brace and produced invalid JSON.
func TestParseAIProductResponseHandlesUnopenedThinkTagWithStrayBraces(t *testing.T) {
	raw := "Note the shape looks like { fields } here.\n" +
		"Let's produce the output.\n</think>\n\n" +
		"{\n  \"generalizedName\": \"Ликер\",\n  \"kcals\": 242,\n  \"protein\": 0.0,\n  \"fat\": 0.0,\n  \"carbs\": 22.0,\n  \"fiber\": 0.0,\n  \"description\": \"Крепкий сладкий алкогольный напиток\"\n}"

	result, err := parseAIProductResponse(raw)
	if err != nil {
		t.Fatalf("parseAIProductResponse() error = %v", err)
	}
	if result.GeneralizedName != "Ликер" || result.Kcals != 242 {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseAIProductResponseHandlesBalancedThinkTag(t *testing.T) {
	raw := "<think>\nreasoning with a stray { brace } pair\n</think>\n" +
		"{\n  \"generalizedName\": \"Йогурт\",\n  \"kcals\": 60,\n  \"protein\": 5,\n  \"fat\": 2,\n  \"carbs\": 4,\n  \"fiber\": 0,\n  \"description\": \"Молочный продукт\"\n}"

	result, err := parseAIProductResponse(raw)
	if err != nil {
		t.Fatalf("parseAIProductResponse() error = %v", err)
	}
	if result.GeneralizedName != "Йогурт" || result.Kcals != 60 {
		t.Fatalf("result = %+v", result)
	}
}

// Go's regexp (RE2) has no backtracking, so greedy `.*` correctness with multiple markers isn't
// obvious by inspection alone — this locks in that trailingThinkTagPattern really does land on the
// LAST </think>, not the first, should a model ever emit more than one reasoning block.
func TestParseAIProductResponseHandlesMultipleThinkTags(t *testing.T) {
	raw := "</think> stray { brace } </think>\n" +
		"{\"generalizedName\":\"Сыр\",\"kcals\":350,\"protein\":25,\"fat\":27,\"carbs\":0,\"fiber\":0,\"description\":\"Твёрдый сыр\"}"

	result, err := parseAIProductResponse(raw)
	if err != nil {
		t.Fatalf("parseAIProductResponse() error = %v", err)
	}
	if result.GeneralizedName != "Сыр" || result.Kcals != 350 {
		t.Fatalf("result = %+v", result)
	}
}

// No </think> marker at all — the pre-existing plain/fenced JSON path must keep working
// unchanged for non-reasoning models.
func TestParseAIProductResponseStillHandlesPlainJSONWithoutThinkTag(t *testing.T) {
	raw := "Some preamble mentioning {unrelated} braces.\n" +
		"{\"generalizedName\":\"Хлеб\",\"kcals\":250,\"protein\":8,\"fat\":1,\"carbs\":50,\"fiber\":3,\"description\":\"Пшеничный хлеб\"}"

	result, err := parseAIProductResponse(raw)
	if err != nil {
		t.Fatalf("parseAIProductResponse() error = %v", err)
	}
	if result.GeneralizedName != "Хлеб" || result.Kcals != 250 {
		t.Fatalf("result = %+v", result)
	}
}
