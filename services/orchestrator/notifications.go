package main

// Sprint 31 : notifications e-mail aux clients.
//   1. "Job termine" : quand tous les chunks d un job sont dans un
//      etat terminal, le proprietaire de l org recoit un e-mail
//      (reussites / echecs, cout reel). Un job annule par le client
//      lui-meme ne declenche PAS d e-mail (il le sait deja).
//   2. "Credits bas" : quand le solde passe sous 5 EUR (decision
//      produit du 8 nov.), UNE seule alerte jusqu au prochain
//      rechargement.
// Les deux notifications sont desactivables depuis la console client
// (cases a cocher, preferences persistees). L envoi part en goroutine
// : la completion d un chunk n attend jamais Brevo.
//
// Decisions produit (Pierre, 8 nov. 2026) : seuil credits bas = 5 EUR,
// opt-out possible depuis la console.

import (
	"fmt"
	"sync"
)

// lowCreditsThresholdEur : seuil d alerte "credits bas" (EUR restants).
const lowCreditsThresholdEur = 5.0

type delivery struct {
	Kind  string // job_done | low_credits
	Email string
	Ref   string // job_id ou org_id
}

// Notifier : file des notifications clients. Le champ deliveries sert
// uniquement de journal interne verifiable par les tests.
type Notifier struct {
	mu         sync.Mutex
	accounts   *AccountStore
	emails     *Emailer
	sent       map[string]bool // job_id -> e-mail "job termine" deja emis
	lowAlerted map[string]bool // org_id -> alerte "credits bas" deja emise
	deliveries []delivery
}

// notifier : instance globale branchee par main.go (meme pattern que
// setDashStore : evite de changer la signature de NewChunker utilisee
// partout dans les tests).
var notifier *Notifier

func setNotifier(n *Notifier) { notifier = n }

func NewNotifier(a *AccountStore, e *Emailer) *Notifier {
	return &Notifier{
		accounts:   a,
		emails:     e,
		sent:       make(map[string]bool),
		lowAlerted: make(map[string]bool),
	}
}

// Deliveries : copie du journal interne (tests / diagnostic).
func (n *Notifier) Deliveries() []delivery {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]delivery, len(n.deliveries))
	copy(out, n.deliveries)
	return out
}

// record memorise une notification decidee (avant l envoi reelle).
func (n *Notifier) record(kind, email, ref string) {
	n.mu.Lock()
	n.deliveries = append(n.deliveries, delivery{kind, email, ref})
	n.mu.Unlock()
}

// JobFinished : tous les chunks du job sont terminaux. Appel depuis
// Chunker.Complete (et le re-scheduler) sous le verrou du chunker :
// les seuls acces ici sont rapides (maps + lecture compte), l envoi
// Brevo part en goroutine.
func (n *Notifier) JobFinished(orgID, jobID string, done, failed, canceled int, costEur float64, doneChunks []Chunk) {
	if n == nil || orgID == "" || jobID == "" {
		return
	}
	// Job annule par le client : aucune tache aboutie ni echouee,
	// rien a lui annoncer qu il ne sache deja.
	if done+failed == 0 {
		return
	}
	n.mu.Lock()
	if n.sent[jobID] {
		n.mu.Unlock()
		return
	}
	n.sent[jobID] = true
	n.mu.Unlock()

	u := n.accounts.UserByOrg(orgID)
	if u == nil || !u.EmailVerified || !u.NotifyJobDone {
		return
	}
	n.record("job_done", u.Email, jobID)
	dashLog("notif", fmt.Sprintf("job %s termine : e-mail programme pour %s (%d ok, %d echecs)",
		jobID, u.Email, done, failed))
	go n.emails.SendJobDone(u.Email, jobID, done, failed, costEur, doneChunks)
}

// MaybeLowCredits : appele apres chaque facturation de chunk. Le flag
// d alerte se rearme tout seul des que le solde remonte au-dessus du
// seuil (pas besoin d un branchement cote rechargement admin).
func (n *Notifier) MaybeLowCredits(orgID string, balance float64) {
	if n == nil || orgID == "" {
		return
	}
	n.mu.Lock()
	if balance >= lowCreditsThresholdEur {
		n.lowAlerted[orgID] = false
		n.mu.Unlock()
		return
	}
	if n.lowAlerted[orgID] {
		n.mu.Unlock()
		return
	}
	n.lowAlerted[orgID] = true
	n.mu.Unlock()

	u := n.accounts.UserByOrg(orgID)
	if u == nil || !u.EmailVerified || !u.NotifyLowCredits {
		return
	}
	n.record("low_credits", u.Email, orgID)
	dashLog("notif", fmt.Sprintf("credits bas (%.2f EUR) pour %s : e-mail programme pour %s",
		balance, orgID, u.Email))
	go n.emails.SendLowCredits(u.Email, balance)
}

// ResetLowCredits : rearmement explicite apres un rechargement admin
// (double securite avec le rearmement automatique).
func (n *Notifier) ResetLowCredits(orgID string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.lowAlerted[orgID] = false
	n.mu.Unlock()
}
