package relay

import "testing"

func TestClassifyAttemptResultPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		result attemptResult
		want   attemptAction
	}{
		{name: "continue", result: attemptResult{}, want: attemptActionContinue},
		{name: "written", result: attemptResult{Written: true}, want: attemptActionWritten},
		{name: "reset beats written", result: attemptResult{ResetConversation: true, Written: true}, want: attemptActionResetConversation},
		{name: "cancel beats reset", result: attemptResult{Canceled: true, ResetConversation: true, Written: true}, want: attemptActionCanceled},
		{name: "success beats terminal failures", result: attemptResult{Success: true, Canceled: true, ResetConversation: true, Written: true}, want: attemptActionSuccess},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyAttemptResult(test.result); got != test.want {
				t.Fatalf("classifyAttemptResult() = %d, want %d", got, test.want)
			}
		})
	}
}
