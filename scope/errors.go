package scope

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Kind says why a task stopped without a result.
type Kind uint8

const (
	// Fail means the task returned an error.
	Fail Kind = iota
	// Die means the task panicked. The error is a *Panic.
	Die
	// Interrupt means the task was cancelled before it finished.
	Interrupt
)

func (k Kind) String() string {
	switch k {
	case Fail:
		return "Fail"
	case Die:
		return "Die"
	case Interrupt:
		return "Interrupt"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// KindOf classifies an error returned by a task. Any error that isn't a
// panic, a timeout or a cancellation is a Fail.
func KindOf(err error) Kind {
	if _, ok := errors.AsType[*Panic](err); ok {
		return Die
	}
	if _, ok := errors.AsType[*TimeoutError](err); ok {
		return Fail
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Interrupt
	}
	return Fail
}

// Panic is a panic recovered from a task, with the stack of the goroutine
// that panicked. Functions in this package re-panic with it in the caller's
// goroutine, so recovery middleware sees the original stack.
type Panic struct {
	Value any
	Stack []byte
}

func (p *Panic) Error() string { return fmt.Sprintf("panic: %v\n\n%s", p.Value, p.Stack) }

// Unwrap returns the panic value when it is an error.
func (p *Panic) Unwrap() error {
	if err, ok := p.Value.(error); ok {
		return err
	}
	return nil
}

// TimeoutError is returned by Timeout when the task runs past its deadline.
// It matches context.DeadlineExceeded with errors.Is.
type TimeoutError struct {
	After time.Duration
}

func (e *TimeoutError) Error() string        { return fmt.Sprintf("timed out after %v", e.After) }
func (e *TimeoutError) Is(target error) bool { return target == context.DeadlineExceeded }

// interrupted is a cancellation cause. It matches context.Canceled, so a
// task that returns context.Cause(ctx) is still seen as interrupted.
type interrupted struct{ msg string }

func (e *interrupted) Error() string        { return e.msg }
func (e *interrupted) Is(target error) bool { return target == context.Canceled }

// ErrInterrupted is the cause used by Fiber.Interrupt when none is given.
var ErrInterrupted error = &interrupted{"scope: interrupted"}

var (
	errSibling error = &interrupted{"scope: a sibling task failed"}
	errLost    error = &interrupted{"scope: another task won the race"}
	errClosed  error = &interrupted{"scope: the scope closed"}
)

func isInterrupt(err error) bool { return KindOf(err) == Interrupt }
