package main

import "testing"

// Sprint 24 : en mode memoire (pas de BDD), dashLog avec dashStore
// nil ne panique pas et le ring buffer continue d'alimenter le
// dashboard. La persistance ne doit jamais casser le journal live.
func TestDashLogSansStore(t *testing.T) {
	setDashStore(nil)
	before := len(dashRecent(300))
	dashLog("job_submit", "test sprint 24 sans store")
	after := dashRecent(300)
	if len(after) != before+1 {
		t.Fatalf("le ring buffer doit contenir 1 evenement de plus (%d -> %d)",
			before, len(after))
	}
	last := after[0]
	if last.Type != "job_submit" || last.Msg != "test sprint 24 sans store" {
		t.Fatalf("evenement mal enregistre : %+v", last)
	}
}

// Sprint 24 : OrgJobs sans BDD (LoadOrgJobs desactive) retourne les
// jobs memoire uniquement, sans panique — le mode degrade reste
// fonctionnel.
func TestOrgJobsSansBDD(t *testing.T) {
	setDashStore(nil)
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, false)
	chunker.Submit(Job{ID: "job-m1", Command: "echo", TaskCount: 2}, "org-m")
	chunker.Submit(Job{ID: "job-m2", Command: "echo", TaskCount: 1}, "org-m")
	jobs := chunker.OrgJobs("org-m", 50)
	if len(jobs) != 2 {
		t.Fatalf("2 jobs attendus en memoire, vu %d", len(jobs))
	}
	// La limite garde les plus recents.
	limited := chunker.OrgJobs("org-m", 1)
	if len(limited) != 1 || limited[0]["job_id"] != "job-m2" {
		t.Fatalf("limite 1 doit garder job-m2, vu %v", limited)
	}
}
