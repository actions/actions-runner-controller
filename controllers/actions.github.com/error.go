package actionsgithubcom

type controllerError string

func (e controllerError) Error() string {
	return string(e)
}

const (
	retryableError = controllerError("retryable error")
	fatalError     = controllerError("fatal error")
)
