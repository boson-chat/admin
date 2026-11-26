package updates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Run states.
const (
	StateRunning        = "running"
	StateRestarting     = "restarting" // handed off to the panel's new version
	StateSucceeded      = "succeeded"
	StateFailed         = "failed"
	StateRolledBack     = "rolled_back"
	StateRollbackFailed = "rollback_failed"
)

// Step states.
const (
	StepPending = "pending"
	StepRunning = "running"
	StepOK      = "ok"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// Step is one stage of a run.
type Step struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	State      string     `json:"state"`
	Detail     string     `json:"detail,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Item is one component in a run.
type Item struct {
	Component string   `json:"component"`
	Name      string   `json:"name"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	FromImage string   `json:"from_image"`
	ToImage   string   `json:"to_image"`
	EnvVar    string   `json:"env_var"`
	Services  []string `json:"services"`
	Pre       []string `json:"pre,omitempty"`
	Self      bool     `json:"self,omitempty"`
	// PinnedImage is the exact previous image (tag@digest) a rollback restores.
	PinnedImage string   `json:"pinned_image,omitempty"`
	Databases   []string `json:"databases,omitempty"`
	Volumes     []string `json:"volumes,omitempty"`
}

// BackupFile is one file of a backup.
type BackupFile struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// ImageRecord is what ran before the update.
type ImageRecord struct {
	Service string   `json:"service"`
	Ref     string   `json:"ref"`
	ID      string   `json:"id"`
	Digests []string `json:"digests,omitempty"`
}

// Backup is what a run saved before changing anything.
type Backup struct {
	Dir        string            `json:"dir"`
	Bytes      int64             `json:"bytes"`
	Files      []BackupFile      `json:"files"`
	Images     []ImageRecord     `json:"images"`
	Migrations map[string]string `json:"migrations,omitempty"`
	Pruned     []string          `json:"pruned,omitempty"`
	FreeBytes  int64             `json:"free_bytes,omitempty"`
}

// Check is one post-update check.
type Check struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Run is an update (or a rollback) with its progress.
type Run struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"` // update | rollback
	Actor      string     `json:"actor"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Items      []Item     `json:"items"`
	Skipped    []string   `json:"skipped,omitempty"`
	Summary    string     `json:"summary"`
	Steps      []Step     `json:"steps"`
	Checks     []Check    `json:"checks"`
	Backup     *Backup    `json:"backup,omitempty"`
	// MigrationsRan: databases whose migration tables changed in this run.
	MigrationsRan []string `json:"migrations_ran,omitempty"`
	// RestoreData: the rollback restores the databases and volumes.
	RestoreData bool   `json:"restore_data,omitempty"`
	Error       string `json:"error,omitempty"`
	// Changed: the run got as far as changing the stack (a rollback makes sense).
	Changed      bool   `json:"changed"`
	CanRollback  bool   `json:"can_rollback"`
	RollbackOf   string `json:"rollback_of,omitempty"`
	RolledBackBy string `json:"rolled_back_by,omitempty"`
	// Handoff is the detached helper job the panel's new version collects.
	Handoff  string    `json:"handoff,omitempty"`
	Location *Location `json:"location,omitempty"`
	Log      []string  `json:"log"`
}

// RunSummary is a run in the history (who applied what).
type RunSummary struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Actor      string     `json:"actor"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Items      []Item     `json:"items"`
	Error      string     `json:"error,omitempty"`
	BackupDir  string     `json:"backup_dir,omitempty"`
}

func (r *Run) clone() *Run {
	b, _ := json.Marshal(r)
	var c Run
	_ = json.Unmarshal(b, &c)
	return &c
}

func (r *Run) summary() RunSummary {
	s := RunSummary{ID: r.ID, Kind: r.Kind, Actor: r.Actor, State: r.State, StartedAt: r.StartedAt,
		FinishedAt: r.FinishedAt, Items: r.Items, Error: r.Error}
	if r.Backup != nil {
		s.BackupDir = r.Backup.Dir
	}
	return s
}

func (r *Run) step(id string) *Step {
	for i := range r.Steps {
		if r.Steps[i].ID == id {
			return &r.Steps[i]
		}
	}
	return nil
}

// journal is the current run and the history, saved as JSON in StateDir.
type journal struct {
	dir     string
	Current *Run         `json:"current,omitempty"`
	History []RunSummary `json:"history"`
	// Runs keeps finished runs that can still be rolled back (by ID).
	Runs map[string]*Run `json:"runs,omitempty"`
}

const historyLen = 30

func loadJournal(dir string) journal {
	j := journal{dir: dir, Runs: map[string]*Run{}}
	if dir == "" {
		return j
	}
	b, err := os.ReadFile(filepath.Join(dir, "updates.json"))
	if err != nil {
		return j
	}
	_ = json.Unmarshal(b, &j)
	j.dir = dir
	if j.Runs == nil {
		j.Runs = map[string]*Run{}
	}
	return j
}

func (j *journal) save() error {
	if j.dir == "" {
		return nil
	}
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(j.dir, ".updates.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(j.dir, "updates.json"))
}

// finish moves a run into the history.
func (j *journal) finish(r *Run) {
	j.History = slices.DeleteFunc(j.History, func(h RunSummary) bool { return h.ID == r.ID })
	j.History = append([]RunSummary{r.summary()}, j.History...)
	if len(j.History) > historyLen {
		j.History = j.History[:historyLen]
	}
	j.Runs[r.ID] = r.clone()
	// Keep the last few full runs only.
	if len(j.Runs) > 5 {
		var oldest string
		for id, x := range j.Runs {
			if oldest == "" || x.StartedAt.Before(j.Runs[oldest].StartedAt) {
				oldest = id
			}
		}
		delete(j.Runs, oldest)
	}
}
