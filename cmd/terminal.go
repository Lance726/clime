package cmd

import (
	"fmt"

	uicli "github.com/alperdrsnn/clime"
)

type Terminal struct{}

var terminal Terminal

func (Terminal) Info(message string) {
	fmt.Println(uicli.DimColor.Sprint("ℹ " + message))
}

func (t Terminal) Infof(format string, args ...any) {
	t.Info(fmt.Sprintf(format, args...))
}

func (Terminal) Error(message string) {
	uicli.ErrorLine(message)
}

func (t Terminal) Errorf(format string, args ...any) {
	t.Error(fmt.Sprintf(format, args...))
}

func (Terminal) Success(message string) {
	uicli.SuccessLine(message)
}

func (t Terminal) Successf(format string, args ...any) {
	t.Success(fmt.Sprintf(format, args...))
}

func (Terminal) Warning(message string) {
	uicli.WarningLine(message)
}

func (t Terminal) Warningf(format string, args ...any) {
	t.Warning(fmt.Sprintf(format, args...))
}

// startSpinner creates and starts a spinner with the project's standard
// dots-cyan style. The message is built via fmt.Sprintf when args are given.
func startSpinner(format string, args ...any) *uicli.Spinner {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	return uicli.NewSpinner().
		WithStyle(uicli.SpinnerDots).
		WithColor(uicli.CyanColor).
		WithMessage(msg).
		Start()
}
