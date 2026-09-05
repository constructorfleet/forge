package planningapprove_test

// This file proves that Forge's real store and artifact loader satisfy
// Approver's interfaces, so a production wiring compiles with no adapter.

import (
	"github.com/Teagan42/forge/internal/planningapprove"
	"github.com/Teagan42/forge/internal/planningfs"
	"github.com/Teagan42/forge/internal/repolock"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

var _ planningapprove.Store = (*storage.SQLiteStore)(nil)
var _ planningapprove.ArtifactStore = (*planningfs.FileArtifactLoader)(nil)
var _ planningapprove.Locker = (*repolock.Locker)(nil)
var _ tui.PlanningApprover = (*planningapprove.Approver)(nil)
