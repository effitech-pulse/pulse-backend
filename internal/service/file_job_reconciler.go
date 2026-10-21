package service

import "context"

type FileJobReconciler struct{ store FileJobStore }

func NewFileJobReconciler(store FileJobStore) *FileJobReconciler { return &FileJobReconciler{store} }
func (r *FileJobReconciler) ReconcileOnce(ctx context.Context) error {
	_, e := r.store.ReconcileFileJobs(ctx, 8)
	return e
}
