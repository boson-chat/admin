package updates

import (
	"fmt"
)

// ScriptSelf and SelfEnv let the Docker test run the self-update helper alone.
var ScriptSelf = scriptSelf

func SelfEnv(loc *Location, root, target, fallback string, wait int) map[string]string {
	s := &Service{o: Options{BackupRoot: root, Keep: 1, UtilImage: "busybox:1.37.0"}}
	env := s.env(loc, &Run{ID: "selftest"})
	env["TARGET_SETS"], env["FALLBACK_SETS"], env["SELF_WAIT"] = target, fallback, fmt.Sprint(wait)
	return env
}

// ScriptRollback and RollbackEnv let the Docker test run the rollback
// helper alone.
var ScriptRollback = scriptRollback

func RollbackEnv(loc *Location, root, backupDir string, kv ...string) map[string]string {
	s := &Service{o: Options{BackupRoot: root, Keep: 1, UtilImage: "busybox:1.37.0"}}
	env := s.env(loc, &Run{ID: "rollbacktest", Backup: &Backup{Dir: backupDir}})
	return with(env, kv...)
}
