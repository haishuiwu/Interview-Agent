package evaluation

import (
	"testing"

	"interview-agent/internal/skill"
)

type skillSelectionCase struct {
	ID            string `json:"id"`
	Input         string `json:"input"`
	ExpectedSkill string `json:"expected_skill"`
}

func TestSkillSelectionBenchmark(t *testing.T) {
	cases := loadFixture[[]skillSelectionCase](t, "skill_selection.json")
	registry := skill.NewSkillRegistry()
	registry.Register(skill.NewLogicalThinkingSkill(nil, nil, nil))
	registry.Register(skill.NewCommunicationTrainingSkill(nil, nil, nil))
	registry.Register(skill.NewProblemSolvingSkill(nil, nil, nil))
	registry.Register(skill.NewCriticalThinkingSkill(nil, nil, nil))
	registry.Register(skill.NewReflectionTrainingSkill(nil, nil, nil))

	passed := 0
	for _, sample := range cases {
		matched := registry.Match(sample.Input)
		actual := ""
		if matched != nil {
			actual = matched.Name()
		}
		if actual == sample.ExpectedSkill {
			passed++
		} else {
			t.Logf("[%s] input=%q expected=%s actual=%s", sample.ID, sample.Input, sample.ExpectedSkill, actual)
		}
	}

	accuracy := reportMetric(t, "Skill Accuracy", passed, len(cases))
	if accuracy < 0.90 {
		t.Fatalf("Skill Accuracy %.2f%% is below 90%%", accuracy*100)
	}
}
