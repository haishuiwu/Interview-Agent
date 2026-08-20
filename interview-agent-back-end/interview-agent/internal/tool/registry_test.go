package educationtool

import (
	"context"
	"reflect"
	"testing"
)

func TestNewEducationRegistryRegistersAllTools(t *testing.T) {
	registry, err := NewEducationRegistry(nil)
	if err != nil {
		t.Fatalf("NewEducationRegistry() error = %v", err)
	}

	want := []string{
		"get_student_profile",
		"get_ability_profile",
		"get_growth_history",
		"get_ability_report",
		"search_training_case",
		"recommend_training_task",
		"update_ability_profile",
		"save_growth_record",
	}
	if got := registry.Names(context.Background()); !reflect.DeepEqual(got, want) {
		t.Fatalf("registered tools = %v, want %v", got, want)
	}
}
