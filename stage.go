package nightwatch

// ExecutionStage represents the current stage of request/command execution.
type ExecutionStage string

const (
	StageBootstrap       ExecutionStage = "bootstrap"
	StageBeforeMiddleware ExecutionStage = "before_middleware"
	StageAction          ExecutionStage = "action"
	StageRender          ExecutionStage = "render"
	StageAfterMiddleware ExecutionStage = "after_middleware"
	StageSending         ExecutionStage = "sending"
	StageTerminating     ExecutionStage = "terminating"
	StageEnd             ExecutionStage = "end"
)

// AllStages returns all stages in execution order, used for initializing duration maps.
func AllStages() []ExecutionStage {
	return []ExecutionStage{
		StageBootstrap,
		StageBeforeMiddleware,
		StageAction,
		StageRender,
		StageAfterMiddleware,
		StageSending,
		StageTerminating,
	}
}
