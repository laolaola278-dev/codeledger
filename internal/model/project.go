package model

type AgentPolicy struct {
	UpdateTaskStatus    bool `yaml:"update_task_status" json:"update_task_status"`
	RecordModifiedFiles bool `yaml:"record_modified_files" json:"record_modified_files"`
	RequireTestResult   bool `yaml:"require_test_result" json:"require_test_result"`
	ContextMaxChars     int  `yaml:"context_max_chars" json:"context_max_chars"`
}

type Project struct {
	ID          string      `yaml:"id" json:"id"`
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description" json:"description"`
	Goal        string      `yaml:"goal" json:"goal"`
	CreatedAt   string      `yaml:"created_at" json:"created_at"`
	UpdatedAt   string      `yaml:"updated_at" json:"updated_at"`
	AgentPolicy AgentPolicy `yaml:"agent_policy" json:"agent_policy"`
}

func DefaultProject() Project {
	return Project{
		ID:          "my-project",
		Name:        "My Project",
		Description: "",
		Goal:        "",
		CreatedAt:   "",
		UpdatedAt:   "",
		AgentPolicy: AgentPolicy{
			UpdateTaskStatus:    true,
			RecordModifiedFiles: true,
			RequireTestResult:   false,
			ContextMaxChars:     12000,
		},
	}
}
