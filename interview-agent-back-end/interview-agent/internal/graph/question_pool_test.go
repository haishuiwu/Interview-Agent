package graph

import (
	"testing"

	imodel "interview-agent/internal/model"
)

func q(id, difficulty string) imodel.PlannedQuestion {
	return imodel.PlannedQuestion{ID: id, Difficulty: difficulty}
}

// TestNewQuestionPool_Bucketing 验证分桶与非法难度归入 medium。
func TestNewQuestionPool_Bucketing(t *testing.T) {
	pool := newQuestionPool([]imodel.PlannedQuestion{
		q("e1", "easy"),
		q("m1", "medium"),
		q("h1", "hard"),
		q("x1", ""),        // 空难度 → medium
		q("x2", "unknown"), // 非法难度 → medium
	})

	if got := len(pool.buckets[imodel.DifficultyEasy]); got != 1 {
		t.Errorf("easy 桶应为 1，实际 %d", got)
	}
	if got := len(pool.buckets[imodel.DifficultyMedium]); got != 3 {
		t.Errorf("medium 桶应为 3（含空与非法难度），实际 %d", got)
	}
	if got := len(pool.buckets[imodel.DifficultyHard]); got != 1 {
		t.Errorf("hard 桶应为 1，实际 %d", got)
	}
	if pool.remain != 5 {
		t.Errorf("remain 应为 5，实际 %d", pool.remain)
	}
}

// TestNext_PrefersTargetDifficulty 目标难度桶非空时优先取该难度。
func TestNext_PrefersTargetDifficulty(t *testing.T) {
	pool := newQuestionPool([]imodel.PlannedQuestion{
		q("e1", "easy"), q("m1", "medium"), q("h1", "hard"),
	})

	got, ok := pool.next(imodel.DifficultyHard)
	if !ok || got.ID != "h1" {
		t.Fatalf("目标 hard 应取 h1，实际 ok=%v id=%s", ok, got.ID)
	}
	got, ok = pool.next(imodel.DifficultyEasy)
	if !ok || got.ID != "e1" {
		t.Fatalf("目标 easy 应取 e1，实际 ok=%v id=%s", ok, got.ID)
	}
}

// TestNext_InvalidTargetFallsBackToMedium 传入非法目标难度时，按 medium 的回退顺序取题。
func TestNext_InvalidTargetFallsBackToMedium(t *testing.T) {
	pool := newQuestionPool([]imodel.PlannedQuestion{q("m", "medium")})
	got, ok := pool.next(imodel.DifficultyLevel("bogus"))
	if !ok || got.ID != "m" {
		t.Fatalf("非法目标难度应按 medium 回退顺序取到 m，实际 ok=%v id=%s", ok, got.ID)
	}
}

// TestNext_FallbackWhenBucketEmpty 目标桶空时就近回退。
func TestNext_FallbackWhenBucketEmpty(t *testing.T) {
	// 只有 medium 题，要 hard 时应就近回退到 medium。
	pool := newQuestionPool([]imodel.PlannedQuestion{
		q("m1", "medium"), q("m2", "medium"),
	})
	got, ok := pool.next(imodel.DifficultyHard)
	if !ok || got.ID != "m1" {
		t.Fatalf("hard 桶空应回退 medium 取 m1，实际 ok=%v id=%s", ok, got.ID)
	}

	// 只有 easy 题，要 hard 时回退顺序 hard→medium→easy，应取 easy。
	pool2 := newQuestionPool([]imodel.PlannedQuestion{q("e1", "easy")})
	got, ok = pool2.next(imodel.DifficultyHard)
	if !ok || got.ID != "e1" {
		t.Fatalf("hard 桶空、仅 easy 应取 e1，实际 ok=%v id=%s", ok, got.ID)
	}
}

// TestNext_BucketOrderPreserved 同难度桶内保持原相对顺序（FIFO）。
func TestNext_BucketOrderPreserved(t *testing.T) {
	pool := newQuestionPool([]imodel.PlannedQuestion{
		q("m1", "medium"), q("m2", "medium"), q("m3", "medium"),
	})
	for _, want := range []string{"m1", "m2", "m3"} {
		got, ok := pool.next(imodel.DifficultyMedium)
		if !ok || got.ID != want {
			t.Fatalf("应按 FIFO 取 %s，实际 ok=%v id=%s", want, ok, got.ID)
		}
	}
}

// TestPool_DrainsExactlyOnce 全部取完且每题只取一次，之后 empty。
func TestPool_DrainsExactlyOnce(t *testing.T) {
	input := []imodel.PlannedQuestion{
		q("e1", "easy"), q("m1", "medium"), q("h1", "hard"), q("h2", "hard"),
	}
	pool := newQuestionPool(input)

	seen := map[string]int{}
	count := 0
	for !pool.empty() {
		got, ok := pool.next(imodel.DifficultyMedium)
		if !ok {
			t.Fatal("题池非空却取不到题")
		}
		seen[got.ID]++
		count++
		if count > len(input) {
			t.Fatal("取题次数超过题目总数，疑似死循环")
		}
	}
	if count != len(input) {
		t.Errorf("应取 %d 题，实际 %d", len(input), count)
	}
	for _, item := range input {
		if seen[item.ID] != 1 {
			t.Errorf("题 %s 被取 %d 次，应为 1 次", item.ID, seen[item.ID])
		}
	}
	if _, ok := pool.next(imodel.DifficultyMedium); ok {
		t.Error("题池取空后 next 应返回 ok=false")
	}
}

// TestPool_EasyBucketSustainsConsecutiveLows 验证 easy 档备厚后，
// 面试者持续答不好、连续需要 easy 题时，能连续取满整档而不过早回退到更难的题。
// 对应 basic 阶段 easy/medium/hard 各备 5 道的配置。
func TestPool_EasyBucketSustainsConsecutiveLows(t *testing.T) {
	var qs []imodel.PlannedQuestion
	for i := 0; i < 5; i++ {
		qs = append(qs, q("e", "easy"), q("m", "medium"), q("h", "hard"))
	}
	pool := newQuestionPool(qs)

	// 持续答不好 → 一直要 easy，应能连续取到 5 道 easy
	for i := 0; i < 5; i++ {
		got, ok := pool.next(imodel.DifficultyEasy)
		if !ok || got.Difficulty != "easy" {
			t.Fatalf("第 %d 次要 easy 应仍取到 easy，实际 ok=%v difficulty=%s", i+1, ok, got.Difficulty)
		}
	}
	// 第 6 次 easy 档已空，才就近回退到 medium
	got, ok := pool.next(imodel.DifficultyEasy)
	if !ok || got.Difficulty != "medium" {
		t.Fatalf("easy 档耗尽后应回退 medium，实际 ok=%v difficulty=%s", ok, got.Difficulty)
	}
}

// TestPool_DifficultyAdjustmentScenario 模拟一段难度调节序列，验证按难度自适应取题。
func TestPool_DifficultyAdjustmentScenario(t *testing.T) {
	pool := newQuestionPool([]imodel.PlannedQuestion{
		q("e1", "easy"), q("e2", "easy"),
		q("m1", "medium"), q("m2", "medium"),
		q("h1", "hard"), q("h2", "hard"),
	})

	// 初始 medium → 取 m1
	if got, _ := pool.next(imodel.DifficultyMedium); got.ID != "m1" {
		t.Fatalf("第1题应为 m1，实际 %s", got.ID)
	}
	// 答得好，升 hard → 取 h1
	if got, _ := pool.next(imodel.DifficultyHard); got.ID != "h1" {
		t.Fatalf("第2题应为 h1，实际 %s", got.ID)
	}
	// 又答得好，保持 hard → 取 h2
	if got, _ := pool.next(imodel.DifficultyHard); got.ID != "h2" {
		t.Fatalf("第3题应为 h2，实际 %s", got.ID)
	}
	// 答错，降 medium → 取 m2
	if got, _ := pool.next(imodel.DifficultyMedium); got.ID != "m2" {
		t.Fatalf("第4题应为 m2，实际 %s", got.ID)
	}
	// 又答错，降 easy → 取 e1
	if got, _ := pool.next(imodel.DifficultyEasy); got.ID != "e1" {
		t.Fatalf("第5题应为 e1，实际 %s", got.ID)
	}
}
