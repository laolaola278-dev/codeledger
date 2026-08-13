package cmd

import "github.com/codeledger/codeledger/internal/planning"

// resetCheckFlags resets all check command flags to their default values.
func resetCheckFlags() {
	checkJSON = false
	checkVerbose = false
	checkStrict = false
}

// resetFinishFlags resets all finish command flags to their default values.
func resetFinishFlags() {
	finishTask = ""
	finishFiles = ""
	finishTest = ""
	finishResult = ""
	finishNote = ""
	finishAgent = ""
	finishAutoFiles = false
	finishCaptureDiff = false
	finishSkipContext = false
	finishSkipReport = false
	finishStrict = false
	finishJSON = false
}

// resetAddFlags resets all add command flags to their default values.
func resetAddFlags() {
	addDescription = ""
	addPriority = "medium"
	addDepends = ""
}

// resetClaimFlags resets all claim command flags to their default values.
func resetClaimFlags() {
	claimAgent = ""
	claimRole = "developer"
	claimTTL = "120m"
}

// resetDoneFlags resets all done command flags to their default values.
func resetDoneFlags() {
	doneFiles = ""
	doneTest = ""
	doneResult = ""
	doneNote = ""
	doneAutoFiles = false
	doneCaptureDiff = false
}

// resetNextFlags resets all next command flags to their default values.
func resetNextFlags() {
	nextRole = ""
	nextJSON = false
}

// resetPlanFlags resets all plan command flags to their default values.
func resetPlanFlags() {
	planMode = planning.PromptModePlanning
	planAgent = ""
	planInput = ""
	planFile = ""
	planJSON = false
	planBy = ""
	planPrompt = false
	planSavePrompt = ""
	planPromptFile = ""
	planSaveMode = ""
}

// resetAllFlags resets all command flags to their default values.
func resetAllFlags() {
	resetCheckFlags()
	resetFinishFlags()
	resetAddFlags()
	resetClaimFlags()
	resetDoneFlags()
	resetNextFlags()
	resetPlanFlags()
}
