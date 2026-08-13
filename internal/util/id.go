package util

import (
	"fmt"
	"regexp"
	"strconv"
)

var taskIDPattern = regexp.MustCompile(`^TASK-(\d+)$`)

// NextTaskID generates the next task ID (e.g. TASK-001 after TASK-003).
func NextTaskID(existingIDs []string) string {
	maxNum := 0
	for _, id := range existingIDs {
		matches := taskIDPattern.FindStringSubmatch(id)
		if len(matches) == 2 {
			num, err := strconv.Atoi(matches[1])
			if err == nil && num > maxNum {
				maxNum = num
			}
		}
	}
	return fmt.Sprintf("TASK-%03d", maxNum+1)
}

// IsValidTaskID checks if a string matches the TASK-XXX format.
func IsValidTaskID(id string) bool {
	return taskIDPattern.MatchString(id)
}
