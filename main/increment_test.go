package main

import (
	"testing"
)

func TestPrefixAndPostfixIncrementAndDecrement(t *testing.T) {
	got := runScriptLogs(t, `
var value = 3;
log value++;
log value;
log ++value;
log value;
log value--;
log value;
log --value;
log value;

var items = [10, 20];
log items[1]++ + 2;
log items[1];
`)
	want := []string{"3", "4", "5", "5", "5", "4", "3", "3", "22", "21"}
	if !equalStrings(got, want) {
		t.Fatalf("increment/decrement values mismatch\n got: %q\nwant: %q", got, want)
	}
}
