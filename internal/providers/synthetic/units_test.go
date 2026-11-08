package synthetic

import "testing"

func TestUnitCounter(t *testing.T) {
	c, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	count := UnitCounter(c)
	// Measured live: the streamed prompt 1482 was 3518 real tokens for DeepSeek V4.1.
	if got, ok := count("hf:deepseek-ai/DeepSeek-V4.1-Flash-1017", 1482, nil); !ok || got < 3500 || got > 3530 {
		t.Fatalf("deepseek = %d %v", got, ok)
	}
	// Aliases resolve through the catalog's hugging_face_id.
	if got, _ := count("syn:large:text", 1482, nil); got < 3500 || got > 3530 {
		t.Fatalf("alias = %d", got)
	}
	if got, _ := count("hf:openai/gpt-oss-120b", 8306, nil); got < 3390 || got > 3410 {
		t.Fatalf("gpt-oss = %d", got)
	}
	if got, ok := count("hf:new/model", 100, []byte("1234567")); !ok || got != 2 {
		t.Fatalf("estimate = %d %v", got, ok)
	}
	if _, ok := count("hf:new/model", 100, nil); ok {
		t.Fatal("estimate without a body")
	}
}
