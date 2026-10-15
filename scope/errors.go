package scope

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
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

// KindOf classifies an error returned by a task: a *Panic is a Die, a
// cancellation is an Interrupt, and any other error, a *TimeoutError
// included, is a Fail. An error that joins several is an Interrupt only if
// each of them is: All's error when a task failed while being cancelled is
// a Fail. err must not be nil.
func KindOf(err error) Kind {
	if _, ok := errors.AsType[*Panic](err); ok {
		return Die
	}
	if _, ok := errors.AsType[*TimeoutError](err); ok {
		return Fail
	}
	if isEach(err, context.Canceled, context.DeadlineExceeded) {
		return Interrupt
	}
	return Fail
}

// isEach reports whether err matches one of targets, as errors.Is would,
// except that an error joining several (with an Unwrap() []error method)
// matches only if each of them does.
func isEach(err error, targets ...error) bool {
	for err != nil {
		for _, t := range targets {
			if t != nil && reflect.TypeOf(t).Comparable() && err == t {
				return true
			}
			if x, ok := err.(interface{ Is(error) bool }); ok && x.Is(t) {
				return true
			}
		}
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			errs := x.Unwrap()
			return len(errs) > 0 && !slices.ContainsFunc(errs, func(err error) bool { return !isEach(err, targets...) })
		default:
			return false
		}
	}
	return false
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
	errClosed  error = &interrupted{"scope: closed"}
)

func isInterrupt(err error) bool { return KindOf(err) == Interrupt }
