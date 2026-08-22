package graph

import (
	"testing"

	"interview-agent/internal/agent"
	imodel "interview-agent/internal/model"
)

// qt 构造带题型和难度的候选题。
func qt(typ, difficulty string) imodel.PlannedQuestion {
	return imodel.PlannedQuestion{Type: typ, Difficulty: difficulty}
}

// realAdjust 返回基于生产逻辑 QuestionPlanner.AdjustDifficulty 的难度调节函数
// （该方法为纯函数，不依赖 chatModel，传 nil 即可）。测试用真实逻辑而非副本。
func realAdjust() adjustFunc {
	planner := agent.NewQuestionPlanner(nil)
	return func(cur imodel.DifficultyLevel, consecRight, consecWrong int) imodel.DifficultyLevel {
		return planner.AdjustDifficulty(&imodel.TrainingState{
			CurrentDifficulty: cur,
			ConsecutiveRight:  consecRight,
			ConsecutiveWrong:  consecWrong,
		})
	}
}

// fullCandidates 构造与生产配额一致的候选池：
// basic easy5/medium5/hard5，experience easy4/medium4/hard4，design medium2/hard2。
func fullCandidates() []imodel.PlannedQuestion {
	var qs []imodel.PlannedQuestion
	add := func(typ, diff string, n int) {
		for i := 0; i < n; i++ {
			qs = append(qs, qt(typ, diff))
		}
	}
	add("basic", "easy", 5)
	add("basic", "medium", 5)
	add("basic", "hard", 5)
	add("experience", "easy", 4)
	add("experience", "medium", 4)
	add("experience", "hard", 4)
	add("design", "medium", 2)
	add("design", "hard", 2)
	return qs
}

type askedQ struct {
	q    imodel.PlannedQuestion
	diff imodel.DifficultyLevel
}

// driveScheduler 跑完整场调度，scoreFn 模拟每题得分（按提问序号与目标难度）。
func driveScheduler(s *stageScheduler, scoreFn func(n int, diff imodel.DifficultyLevel) float64) []askedQ {
	var out []askedQ
	for {
		q, diff, done := s.next()
		if done {
			break
		}
		out = append(out, askedQ{q, diff})
		s.record(scoreFn(len(out), diff))
		if len(out) > 500 {
			panic("scheduler 未终止，疑似死循环")
		}
	}
	return out
}

func allCorrect(int, imodel.DifficultyLevel) float64 { return 100 }
func allWrong(int, imodel.DifficultyLevel) float64   { return 0 }

// TestScheduler_TotalToAsk 实际提问总数 = 每阶段 min(askNum, 库存) 之和。
func TestScheduler_TotalToAsk(t *testing.T) {
	s := newStageScheduler(defaultStages, fullCandidates(), realAdjust())
	if got := s.totalToAsk(); got != 15 { // 8 + 5 + 2
		t.Errorf("totalToAsk=%d，期望 15", got)
	}
}

// TestScheduler_StageOrderAndCounts 阶段顺序 basic→experience→design，每阶段抽满 askNum。
func TestScheduler_StageOrderAndCounts(t *testing.T) {
	s := newStageScheduler(defaultStages, fullCandidates(), realAdjust())
	got := driveScheduler(s, allCorrect)

	if len(got) != 15 {
		t.Fatalf("应提问 15 道，实际 %d", len(got))
	}
	for i, a := range got {
		var want string
		switch {
		case i < 8:
			want = "basic"
		case i < 13:
			want = "experience"
		default:
			want = "design"
		}
		if a.q.Type != want {
			t.Errorf("第 %d 道应为 %s 阶段，实际 %s", i+1, want, a.q.Type)
		}
	}
}

// TestScheduler_ResetDifficultyBetweenStages 阶段切换必须把难度重置为 medium，不继承上一阶段。
func TestScheduler_ResetDifficultyBetweenStages(t *testing.T) {
	s := newStageScheduler(defaultStages, fullCandidates(), realAdjust())
	got := driveScheduler(s, allWrong) // 全程答错，basic 阶段会一路降到 easy

	// basic 阶段内：连错 2 题后第 3 题应降到 easy（证明阶段内确实在降）
	if got[2].diff != imodel.DifficultyEasy {
		t.Errorf("basic 第 3 题应降到 easy，实际 %s", got[2].diff)
	}
	// 进入 experience 阶段（第 9 道）：难度必须重置为 medium，而不是继承 basic 末尾的 easy
	if got[8].q.Type != "experience" {
		t.Fatalf("第 9 道应进入 experience 阶段，实际 %s", got[8].q.Type)
	}
	if got[8].diff != imodel.DifficultyMedium {
		t.Errorf("阶段切换应把难度重置为 medium，实际 %s（说明错误地继承了上一阶段）", got[8].diff)
	}
	// 进入 design 阶段（第 14 道）同样重置
	if got[13].q.Type != "design" {
		t.Fatalf("第 14 道应进入 design 阶段，实际 %s", got[13].q.Type)
	}
	if got[13].diff != imodel.DifficultyMedium {
		t.Errorf("design 阶段难度也应重置为 medium，实际 %s", got[13].diff)
	}
}

// TestScheduler_SustainedLowStaysEasy 持续答错时，basic 阶段能连续取满 5 道 easy 候选，且不串到下一阶段。
func TestScheduler_SustainedLowStaysEasy(t *testing.T) {
	s := newStageScheduler(defaultStages, fullCandidates(), realAdjust())
	got := driveScheduler(s, allWrong)

	basic := got[:8]
	easyCount := 0
	for _, a := range basic {
		if a.q.Type != "basic" {
			t.Errorf("basic 阶段混入了 %s 题", a.q.Type)
		}
		if a.q.Difficulty == "easy" {
			easyCount++
		}
	}
	// easy 备 5 道，持续答错应把这 5 道 easy 全部取到（备厚生效）
	if easyCount != 5 {
		t.Errorf("basic 全错应取满 5 道 easy 候选，实际 %d", easyCount)
	}
}

// TestScheduler_SustainedHighStaysHard 持续答对时，basic 阶段能连续取满 5 道 hard 候选。
func TestScheduler_SustainedHighStaysHard(t *testing.T) {
	s := newStageScheduler(defaultStages, fullCandidates(), realAdjust())
	got := driveScheduler(s, allCorrect)

	hardCount := 0
	for _, a := range got[:8] {
		if a.q.Difficulty == "hard" {
			hardCount++
		}
	}
	if hardCount != 5 {
		t.Errorf("basic 全对应取满 5 道 hard 候选，实际 %d", hardCount)
	}
}

// TestScheduler_SkipsEmptyAndShortStages 候选不足的阶段抽完即止，没有候选的阶段直接跳过。
func TestScheduler_SkipsEmptyAndShortStages(t *testing.T) {
	qs := []imodel.PlannedQuestion{
		qt("basic", "easy"), qt("basic", "medium"), qt("basic", "hard"), // 仅 3 道 < 8
		// 无 experience 候选
		qt("design", "medium"), qt("design", "hard"),
	}
	s := newStageScheduler(defaultStages, qs, realAdjust())

	if got := s.totalToAsk(); got != 5 { // 3 + 0 + 2
		t.Errorf("totalToAsk=%d，期望 5", got)
	}

	got := driveScheduler(s, allCorrect)
	if len(got) != 5 {
		t.Fatalf("应提问 5 道，实际 %d", len(got))
	}
	for i := 0; i < 3; i++ {
		if got[i].q.Type != "basic" {
			t.Errorf("第 %d 道应为 basic，实际 %s", i+1, got[i].q.Type)
		}
	}
	// experience 被跳过，直接进 design
	for i := 3; i < 5; i++ {
		if got[i].q.Type != "design" {
			t.Errorf("第 %d 道应为 design（experience 应被跳过），实际 %s", i+1, got[i].q.Type)
		}
	}
}

// TestScheduler_NoCandidates 没有任何候选题时立即结束，不死循环。
func TestScheduler_NoCandidates(t *testing.T) {
	s := newStageScheduler(defaultStages, nil, realAdjust())
	if got := s.totalToAsk(); got != 0 {
		t.Errorf("totalToAsk=%d，期望 0", got)
	}
	got := driveScheduler(s, allCorrect)
	if len(got) != 0 {
		t.Errorf("无候选题应提问 0 道，实际 %d", len(got))
	}
}
