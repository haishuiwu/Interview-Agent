/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

import (
	"math"
	"testing"
)

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestCalcRecallAtK(t *testing.T) {
	tests := []struct {
		name       string
		retrieved  []string
		relevant   []string
		k          int
		wantRecall float64
	}{
		{
			name:       "全部命中",
			retrieved:  []string{"a", "b", "c", "d", "e"},
			relevant:   []string{"a", "b", "c"},
			k:          5,
			wantRecall: 1.0,
		},
		{
			name:       "Top3 命中1个 / 3个相关",
			retrieved:  []string{"a", "x", "y", "b", "c"},
			relevant:   []string{"a", "b", "c"},
			k:          3,
			wantRecall: 1.0 / 3.0,
		},
		{
			name:       "Top5 命中2个 / 3个相关",
			retrieved:  []string{"a", "x", "y", "b", "z"},
			relevant:   []string{"a", "b", "c"},
			k:          5,
			wantRecall: 2.0 / 3.0,
		},
		{
			name:       "未命中",
			retrieved:  []string{"x", "y", "z"},
			relevant:   []string{"a", "b"},
			k:          10,
			wantRecall: 0,
		},
		{
			name:       "K 比 retrieved 大",
			retrieved:  []string{"a", "b"},
			relevant:   []string{"a", "b", "c"},
			k:          10,
			wantRecall: 2.0 / 3.0,
		},
		{
			name:       "空 relevant 返回 0",
			retrieved:  []string{"a", "b"},
			relevant:   []string{},
			k:          10,
			wantRecall: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcRecallAtK(tt.retrieved, tt.relevant, tt.k)
			if !floatEq(got, tt.wantRecall) {
				t.Errorf("calcRecallAtK = %v, want %v", got, tt.wantRecall)
			}
		})
	}
}

func TestCalcMRR(t *testing.T) {
	tests := []struct {
		name      string
		retrieved []string
		relevant  []string
		want      float64
	}{
		{
			name:      "第1位命中",
			retrieved: []string{"a", "b", "c"},
			relevant:  []string{"a"},
			want:      1.0,
		},
		{
			name:      "第2位命中",
			retrieved: []string{"x", "a", "b"},
			relevant:  []string{"a"},
			want:      0.5,
		},
		{
			name:      "第4位命中（多个相关，取第一个）",
			retrieved: []string{"x", "y", "z", "a", "b"},
			relevant:  []string{"a", "b"},
			want:      0.25,
		},
		{
			name:      "未命中",
			retrieved: []string{"x", "y", "z"},
			relevant:  []string{"a"},
			want:      0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcMRR(tt.retrieved, tt.relevant)
			if !floatEq(got, tt.want) {
				t.Errorf("calcMRR = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalcHits(t *testing.T) {
	retrieved := []string{"x", "a", "y", "b", "z"}
	relevant := []string{"a", "b", "c"}
	hits, firstRank := calcHits(retrieved, relevant)
	if len(hits) != 2 || hits[0] != "a" || hits[1] != "b" {
		t.Errorf("calcHits got hits = %v, want [a b]", hits)
	}
	if firstRank != 2 {
		t.Errorf("calcHits firstHit = %d, want 2", firstRank)
	}

	hits2, rank2 := calcHits([]string{"x", "y"}, []string{"a"})
	if len(hits2) != 0 || rank2 != 0 {
		t.Errorf("未命中应返回空 hits 和 firstRank=0, got hits=%v rank=%d", hits2, rank2)
	}
}

func TestAggregateReport(t *testing.T) {
	results := []SampleResult{
		{SampleID: "1", Topic: "Go", Difficulty: "easy", RecallAt10: 1.0, RecallAt20: 1.0, MRR: 1.0},
		{SampleID: "2", Topic: "Go", Difficulty: "hard", RecallAt10: 0.5, RecallAt20: 0.5, MRR: 0.5},
		{SampleID: "3", Topic: "MySQL", Difficulty: "easy", RecallAt10: 0, RecallAt20: 0.5, MRR: 0},
	}
	report := aggregateReport(results, RAGConfig{Note: "test"})
	if report.SampleCount != 3 {
		t.Errorf("SampleCount = %d, want 3", report.SampleCount)
	}
	if !floatEq(report.Overall.RecallAt10, 0.5) {
		t.Errorf("Overall Recall@10 = %v, want 0.5", report.Overall.RecallAt10)
	}
	if !floatEq(report.Overall.MRR, 0.5) {
		t.Errorf("Overall MRR = %v, want 0.5", report.Overall.MRR)
	}
	if !floatEq(report.TopicMetrics["Go"].RecallAt10, 0.75) {
		t.Errorf("Topic[Go] Recall@10 = %v, want 0.75", report.TopicMetrics["Go"].RecallAt10)
	}
	if report.TopicMetrics["MySQL"].Count != 1 {
		t.Errorf("Topic[MySQL] count = %d, want 1", report.TopicMetrics["MySQL"].Count)
	}
	if len(report.WorstSamples) != 3 {
		t.Errorf("WorstSamples length = %d, want 3", len(report.WorstSamples))
	}
	if report.WorstSamples[0].SampleID != "3" {
		t.Errorf("WorstSamples[0] = %s, want '3' (Recall@10 最低)", report.WorstSamples[0].SampleID)
	}
}
