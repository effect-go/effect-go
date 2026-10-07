package errgroup

import "context"

type Group struct{}

func WithContext(ctx context.Context) (*Group, context.Context) { return &Group{}, ctx }
func (g *Group) Go(f func() error)                              {}
func (g *Group) TryGo(f func() error) bool                      { return true }
func (g *Group) Wait() error                                    { return nil }
