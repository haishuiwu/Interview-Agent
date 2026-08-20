/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package graph

import imodel "interview-agent/internal/model"

// stageConfig 单个训练阶段的配置：题型 + 该阶段实际提问数（从候选池抽取的上限）。
type stageConfig struct {
	typ    string
	askNum int
}

// defaultStages 训练阶段顺序与每阶段提问数：教育理论 → 教学实践 → 课堂情境。
var defaultStages = []stageConfig{
	{imodel.QuestionTypeTheory, 8},
	{imodel.QuestionTypePractice, 5},
	{imodel.QuestionTypeScenario, 2},
}

// adjustFunc 依据当前难度与连对/连错次数，返回下一题难度。
// 由调用方注入（生产环境包装 QuestionPlanner.AdjustDifficulty），便于解耦与测试。
type adjustFunc func(cur imodel.DifficultyLevel, consecRight, consecWrong int) imodel.DifficultyLevel

// stageScheduler 面试阶段化取题 + 阶段内难度调节的纯调度逻辑（不含任何 IO）。
//
// 职责：
//   - 按 stages 顺序推进，跳过没有候选题的阶段；
//   - 每个阶段从该阶段候选池按当前难度就近取题，最多取 askNum 道；
//   - 进入新阶段时把难度重置为本轮画像起点（默认 medium）、连对/连错清零；
//   - 依据每题得分调整本阶段内的后续难度。
//
// 把这套逻辑从 nodeInterview 的 IO（提问/评分/回调）中剥离出来，使其可独立测试。
type stageScheduler struct {
	stages []stageConfig
	byType map[string][]imodel.PlannedQuestion
	adjust adjustFunc

	stageIdx   int
	pool       *questionPool
	stageAsked int
	started    bool

	difficulty  imodel.DifficultyLevel
	initial     imodel.DifficultyLevel
	consecRight int
	consecWrong int
}

// newStageScheduler 按题型给候选题分组，构建调度器。
func newStageScheduler(stages []stageConfig, questions []imodel.PlannedQuestion, adjust adjustFunc, initial ...imodel.DifficultyLevel) *stageScheduler {
	normalizedStages := make([]stageConfig, len(stages))
	copy(normalizedStages, stages)
	for i := range normalizedStages {
		normalizedStages[i].typ = imodel.NormalizeQuestionType(normalizedStages[i].typ)
	}
	byType := make(map[string][]imodel.PlannedQuestion)
	for _, q := range questions {
		normalizedType := imodel.NormalizeQuestionType(q.Type)
		byType[normalizedType] = append(byType[normalizedType], q)
	}
	initialDifficulty := imodel.DifficultyMedium
	if len(initial) > 0 {
		switch initial[0] {
		case imodel.DifficultyEasy, imodel.DifficultyMedium, imodel.DifficultyHard:
			initialDifficulty = initial[0]
		}
	}
	return &stageScheduler{
		stages:  normalizedStages,
		byType:  byType,
		adjust:  adjust,
		initial: initialDifficulty,
	}
}

// totalToAsk 预计算实际提问总数（每阶段取 min(askNum, 候选库存) 之和）。
func (s *stageScheduler) totalToAsk() int {
	total := 0
	for _, st := range s.stages {
		if n := len(s.byType[st.typ]); n < st.askNum {
			total += n
		} else {
			total += st.askNum
		}
	}
	return total
}

// next 返回下一道题及其难度；done=true 表示所有阶段都已问完。
// 进入新阶段时内部会自动重置难度状态。
func (s *stageScheduler) next() (q imodel.PlannedQuestion, difficulty imodel.DifficultyLevel, done bool) {
	for {
		// 尚未开始、当前阶段已抽满、或当前池已空 → 推进到下一个可用阶段。
		if s.pool == nil || s.stageAsked >= s.stages[s.stageIdx].askNum || s.pool.empty() {
			if !s.advanceStage() {
				return imodel.PlannedQuestion{}, "", true
			}
		}
		picked, ok := s.pool.next(s.difficulty)
		if !ok {
			s.pool = nil // 当前阶段候选已耗尽，强制推进到下一阶段
			continue
		}
		s.stageAsked++
		return picked, s.difficulty, false
	}
}

// advanceStage 推进到下一个有候选题的阶段，并重置阶段内难度状态。返回 false 表示没有更多阶段。
func (s *stageScheduler) advanceStage() bool {
	from := 0
	if s.started {
		from = s.stageIdx + 1
	}
	for i := from; i < len(s.stages); i++ {
		candidates := s.byType[s.stages[i].typ]
		if len(candidates) == 0 {
			continue
		}
		s.stageIdx = i
		s.pool = newQuestionPool(candidates)
		s.stageAsked = 0
		// 进入新阶段：难度调节独立重置到本轮画像确定的起点，不继承上一阶段表现。
		s.difficulty = s.initial
		s.consecRight = 0
		s.consecWrong = 0
		s.started = true
		return true
	}
	return false
}

// record 反馈本题得分，更新本阶段内难度（供下一次 next 使用）。
// 得分 >= 70 记为答好，否则记为答得不好。
func (s *stageScheduler) record(score float64) {
	if score >= 70 {
		s.consecRight++
		s.consecWrong = 0
	} else {
		s.consecWrong++
		s.consecRight = 0
	}
	s.difficulty = s.adjust(s.difficulty, s.consecRight, s.consecWrong)
}
