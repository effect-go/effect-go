package errgroup

import "context"

type Group struct{}

func WithContext(ctx context.Context) (*Group, context.Context) { return &Group{}, ctx }
func (g *Group) Go(f func() error)                              {}
func (g *Group) Wait() error                                    { return nil }
