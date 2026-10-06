package v1alpha1

import "context"

func (f *Function) SetDefaults(ctx context.Context) {
	if f == nil {
		return
	}

	f.Spec.SetDefaults(ctx)
}

func (s *FunctionSpec) SetDefaults(ctx context.Context) {
	if s == nil {
		return
	}

	s.Repository.SetDefaults(ctx)
	s.Registry.SetDefaults(ctx)
}

func (r *FunctionSpecRepository) SetDefaults(ctx context.Context) {
	if r == nil {
		return
	}
}

func (r *FunctionSpecRegistry) SetDefaults(ctx context.Context) {
	if r == nil {
		return
	}
}
