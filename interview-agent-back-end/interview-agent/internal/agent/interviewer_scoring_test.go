package agent

import "testing"

func TestCalculateAnswerScoreUsesEvidenceCoverage(t *testing.T) {
	if got := calculateAnswerScore("这是我的分析", []string{"观点", "理由", "例子"}, []string{"验证"}); got != 75 {
		t.Fatalf("evidence score = %.2f, want 75", got)
	}
	if got := calculateAnswerScore("不会", []string{"模型误判的命中点"}, nil); got != 0 {
		t.Fatalf("skip answer score = %.2f, want 0", got)
	}
}
