/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package graph

import imodel "interview-agent/internal/model"

// questionPool 按难度分桶的题池，支持按当前难度就近取题。
// 用于让"动态难度调节"真正生效：每答完一题，依据更新后的 CurrentDifficulty
// 从对应难度桶里取下一道题；目标难度桶取空时就近降级/升级到最接近的非空桶。
type questionPool struct {
	buckets map[imodel.DifficultyLevel][]imodel.PlannedQuestion
	remain  int
}

// fallbackOrder 定义每个目标难度取不到题时的就近回退顺序。
var fallbackOrder = map[imodel.DifficultyLevel][]imodel.DifficultyLevel{
	imodel.DifficultyEasy:   {imodel.DifficultyEasy, imodel.DifficultyMedium, imodel.DifficultyHard},
	imodel.DifficultyMedium: {imodel.DifficultyMedium, imodel.DifficultyEasy, imodel.DifficultyHard},
	imodel.DifficultyHard:   {imodel.DifficultyHard, imodel.DifficultyMedium, imodel.DifficultyEasy},
}

// newQuestionPool 把题目按难度分桶，桶内保持原相对顺序。
// Difficulty 不是 easy/medium/hard 的题统一归入 medium 桶。
func newQuestionPool(questions []imodel.PlannedQuestion) *questionPool {
	p := &questionPool{
		buckets: map[imodel.DifficultyLevel][]imodel.PlannedQuestion{
			imodel.DifficultyEasy:   {},
			imodel.DifficultyMedium: {},
			imodel.DifficultyHard:   {},
		},
	}
	for _, q := range questions {
		level := imodel.DifficultyLevel(q.Difficulty)
		if _, ok := p.buckets[level]; !ok {
			level = imodel.DifficultyMedium
		}
		p.buckets[level] = append(p.buckets[level], q)
		p.remain++
	}
	return p
}

// empty 题池是否已取空。
func (p *questionPool) empty() bool {
	return p.remain == 0
}

// next 按目标难度取下一题：优先目标难度桶，空则按 fallbackOrder 就近取。
// 取出的题会从桶中移除。题池为空时返回 ok=false。
func (p *questionPool) next(target imodel.DifficultyLevel) (imodel.PlannedQuestion, bool) {
	order, ok := fallbackOrder[target]
	if !ok {
		order = fallbackOrder[imodel.DifficultyMedium]
	}
	for _, level := range order {
		bucket := p.buckets[level]
		if len(bucket) == 0 {
			continue
		}
		q := bucket[0]
		p.buckets[level] = bucket[1:]
		p.remain--
		return q, true
	}
	return imodel.PlannedQuestion{}, false
}
