package skill

import "testing"

func TestStudentAbilitySkillDefinitions(t *testing.T) {
	tests := []struct {
		skill   *AbilityTrainingSkill
		name    string
		trigger string
	}{
		{NewLogicalThinkingSkill(nil, nil, nil), "logical-thinking", "我想做逻辑训练"},
		{NewCommunicationTrainingSkill(nil, nil, nil), "communication-training", "帮我练习口头表达"},
		{NewProblemSolvingSkill(nil, nil, nil), "problem-solving", "我想做问题解决训练"},
		{NewCriticalThinkingSkill(nil, nil, nil), "critical-thinking", "练习判断信息是否可信"},
		{NewReflectionTrainingSkill(nil, nil, nil), "reflection-training", "帮我复盘学习错误"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			definition := tt.skill.Definition()
			if tt.skill.Name() != tt.name || definition.Name != tt.name {
				t.Fatalf("skill name = %q, definition name = %q", tt.skill.Name(), definition.Name)
			}
			if len(definition.ApplicableScenarios) == 0 || len(definition.TrainingGoals) == 0 ||
				len(definition.AgentBehaviorRules) == 0 || len(definition.EvaluationDimensions) == 0 {
				t.Fatal("training definition must include scenarios, goals, behavior rules and evaluation dimensions")
			}
			if !tt.skill.Match(tt.trigger) {
				t.Fatalf("skill %s did not match %q", tt.name, tt.trigger)
			}
		})
	}
}

func TestStudentAbilitySkillsCanBeRegistered(t *testing.T) {
	registry := NewSkillRegistry()
	registry.Register(NewLogicalThinkingSkill(nil, nil, nil))
	registry.Register(NewCommunicationTrainingSkill(nil, nil, nil))
	registry.Register(NewProblemSolvingSkill(nil, nil, nil))
	registry.Register(NewCriticalThinkingSkill(nil, nil, nil))
	registry.Register(NewReflectionTrainingSkill(nil, nil, nil))

	if got := len(registry.List()); got != 5 {
		t.Fatalf("registered skill count = %d, want 5", got)
	}
	matched := registry.Match("我想练习批判性思维")
	if matched == nil || matched.Name() != "critical-thinking" {
		t.Fatalf("unexpected matched skill: %v", matched)
	}
}
