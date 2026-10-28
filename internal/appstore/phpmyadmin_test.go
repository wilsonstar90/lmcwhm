package appstore

import "testing"

func TestPHPQuote(t *testing.T) {
	if got := phpQuote(`a'b\c`); got != `'a\'b\\c'` {
		t.Fatalf("phpQuote = %s", got)
	}
}
