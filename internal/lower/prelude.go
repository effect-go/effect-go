package lower

// prelude returns the helpers draft code calls where the final code needs
// types that aren't known yet. Their signatures make go/types infer those
// types. They are never part of the output.
func prelude(pkg string) []byte {
	return []byte("package " + pkg + `

import (
	_ego_context "context"
	_ego_time "time"
	_ego_schedule "` + schedulePath + `"
)

func _egoV[T any](v T, _ ...any) T                                  { return v }
func _egoAll2[A, B any](A, B) (a A, b B, err error)                  { return }
func _egoAll3[A, B, C any](A, B, C) (a A, b B, c C, err error)       { return }
func _egoAll4[A, B, C, D any](A, B, C, D) (a A, b B, c C, d D, err error) { return }
func _egoAllN[T any](...T) (v []T, err error)                        { return }
func _egoRace[T any](...T) (v T, err error)                          { return }
func _egoRetry[T any](_ego_schedule.Schedule, T) (v T, err error)    { return }
func _egoTimeout[T any](_ego_time.Duration, T) (v T, err error)      { return }
func _egoIf[T any](bool, T, T) (v T)                                 { return }
func _egoOne[T any](...T) (v T)                                      { return }
func _egoAs[T any](any) (v T)                                        { return }
func _egoCo[T any](T, T) (v T)                                       { return }
func _egoUse(...any)                                                 {}
func _egoEach[R any](int, func() R) (v []R, err error)               { return }
func _egoEachF[T, R any]([]T, int, func(_ego_context.Context, T) (R, error)) (v []R, err error) { return }
`)
}
