package main

// Sprint 8 - Persistance PostgreSQL des etats runtime.
// Mode degrade : si DATABASE_URL est vide ou la BDD injoignable,
// l'orchestrateur continue en memoire (best effort, jamais de crash).
// Sprint 9 - Persiste aussi les organisations clientes (LAI-29).
// Sprint 10 - Persiste les assets clients (LAI-28) et les nouvelles
// colonnes des chunks (attempts, zone, assets) et noeuds (zone).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// Store ecrit l'etat dans PostgreSQL. db == nil => mode memoire.
type Store struct {
	db *sql.DB
}

func NewStore() *Store {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Printf("store: DATABASE_URL absent, mode memoire (rien n'est persiste)")
		return &Store{db: nil}
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		log.Printf("store: ouverture impossible: %v - mode memoire", err)
		return &Store{db: nil}
	}
	// Docker demarre souvent postgres APRES l'orchestrateur : on laisse
	// jusqu'a 30 s a la BDD pour repondre avant de basculer en memoire
	// (sinon un demarrage frais du compose perdait la persistance).
	var pingErr error
	for i := 0; i < 15; i++ {
		if pingErr = db.Ping(); pingErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if pingErr != nil {
		log.Printf("store: BDD injoignable apres 30 s: %v - mode memoire", pingErr)
		db.Close()
		return &Store{db: nil}
	}
	log.Printf("store: PostgreSQL connecte, persistance activee")
	return &Store{db: db}
}

func (s *Store) enabled() bool { return s != nil && s.db != nil }

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nullTime : time.Time nulle -> NULL SQL (Sprint 15c).
func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

// nullTimePtr : *time.Time nil -> NULL SQL (Sprint 15d).
func nullTimePtr(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

// SaveNode insere ou rafraichit un noeud (idempotent).
func (s *Store) SaveNode(n *Node) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_nodes
		(id, gpu_model, gpu_category, zone, machine_fingerprint,
		 trust_score, status, last_seen, online, user_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
		  gpu_model=EXCLUDED.gpu_model,
		  gpu_category=EXCLUDED.gpu_category,
		  zone=EXCLUDED.zone,
		  machine_fingerprint=EXCLUDED.machine_fingerprint,
		  last_seen=EXCLUDED.last_seen,
		  online=EXCLUDED.online,
		  user_id=EXCLUDED.user_id`,
		n.ID, n.GPUModel, n.GPUCategory, n.Zone, n.MachineFingerprint,
		n.TrustScore, n.Status, n.LastSeen, n.Online, n.UserID)
	if err != nil {
		log.Printf("store.SaveNode: %v", err)
	}
}

// SetNodeOnline marque un noeud en ligne ou hors ligne.
func (s *Store) SetNodeOnline(id string, online bool) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`UPDATE runtime_nodes SET online=$2, last_seen=now() WHERE id=$1`,
		id, online)
	if err != nil {
		log.Printf("store.SetNodeOnline: %v", err)
	}
}

// SaveChunk ecrit l'etat complet d'un chunk (insertion ou mise a jour).
func (s *Store) SaveChunk(ch *Chunk) {
	if !s.enabled() {
		return
	}
	args, _ := json.Marshal(ch.Args)
	assets, _ := json.Marshal(ch.AssetIDs)
	_, err := s.db.Exec(`INSERT INTO runtime_chunks
		(id, job_id, node_id, command, args, expected_output, timeout_secs,
		 status, result_sha256, price_eur_per_hour, payout_eur,
		 attempts, zone, assets, duration_secs, sampled, org_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17, now())
		ON CONFLICT (id) DO UPDATE SET
		  status=EXCLUDED.status,
		  result_sha256=EXCLUDED.result_sha256,
		  price_eur_per_hour=EXCLUDED.price_eur_per_hour,
		  payout_eur=EXCLUDED.payout_eur,
		  attempts=EXCLUDED.attempts,
		  node_id=EXCLUDED.node_id,
		  duration_secs=EXCLUDED.duration_secs,
		  sampled=EXCLUDED.sampled,
		  updated_at=now()`,
		ch.ID, ch.JobID, ch.NodeID, ch.Command, string(args), ch.ExpectedOutput,
		ch.TimeoutSecs, ch.Status, nullStr(ch.ResultSHA256),
		ch.PriceEurPerHour, ch.PayoutEur,
		ch.Attempts, nullStr(ch.Zone), string(assets),
		ch.DurationSecs, ch.Sampled, ch.OrgID)
	if err != nil {
		log.Printf("store.SaveChunk: %v", err)
	}
}

// SavePriceChange journalise un changement de tarif.
func (s *Store) SavePriceChange(category string, eurPerHour float64) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`INSERT INTO runtime_price_history (category, eur_per_hour) VALUES ($1,$2)`,
		category, eurPerHour)
	if err != nil {
		log.Printf("store.SavePriceChange: %v", err)
	}
}

// SaveOrg insere ou rafraichit une organisation cliente (LAI-29).
func (s *Store) SaveOrg(org *Org, keyHash string) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_orgs (id, name, api_key_hash, created_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET
		  name=EXCLUDED.name,
		  api_key_hash=EXCLUDED.api_key_hash`,
		org.ID, org.Name, keyHash, org.CreatedAt)
	if err != nil {
		log.Printf("store.SaveOrg: %v", err)
	}
}

// Asset : fichier client stocke par l'orchestrateur (LAI-28).
type Asset struct {
	ID        string `json:"asset_id"`
	OrgID     string `json:"-"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Zone      string `json:"zone"`
}

// SaveAsset enregistre les metadonnees d'un asset (idempotent :
// un contenu identique renvoie le meme asset_id).
func (s *Store) SaveAsset(a *Asset) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_assets
		(id, org_id, filename, sha256, size_bytes, zone)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO NOTHING`,
		a.ID, a.OrgID, a.Filename, a.SHA256, a.SizeBytes, a.Zone)
	if err != nil {
		log.Printf("store.SaveAsset: %v", err)
	}
}

// GetAsset charge les metadonnees d'un asset (nil si inconnu).
func (s *Store) GetAsset(id string) *Asset {
	if !s.enabled() {
		return nil
	}
	row := s.db.QueryRow(`SELECT id, org_id, filename, sha256, size_bytes, zone
		FROM runtime_assets WHERE id=$1`, id)
	var a Asset
	err := row.Scan(&a.ID, &a.OrgID, &a.Filename, &a.SHA256, &a.SizeBytes, &a.Zone)
	if err != nil {
		return nil
	}
	return &a
}

// Sprint 27 : LoadAssets recharge les metadonnees de TOUS les assets
// au demarrage (les fichiers restent sur le volume ASSETS_DIR).
func (s *Store) LoadAssets() []*Asset {
	if !s.enabled() {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, org_id, filename, sha256, size_bytes, zone
		FROM runtime_assets`)
	if err != nil {
		log.Printf("store.LoadAssets: %v", err)
		return nil
	}
	defer rows.Close()
	out := []*Asset{}
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.OrgID, &a.Filename, &a.SHA256,
			&a.SizeBytes, &a.Zone); err == nil {
			out = append(out, &a)
		}
	}
	return out
}

// --- Sprint 12 : wallet & ledger (LAI-34/35/36) ---

// SaveLedger ajoute une ecriture au grand livre (append-only, LAI-34).
func (s *Store) SaveLedger(e *LedgerEntry) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_ledger
		(id, at, node_id, entry_type, amount_eur, ref)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO NOTHING`,
		e.Seq, e.At, e.NodeID, e.EntryType, e.AmountEur, e.Ref)
	if err != nil {
		log.Printf("store.SaveLedger: %v", err)
	}
}

// SaveWithdrawal insere ou rafraichit une demande de retrait (LAI-35).
func (s *Store) SaveWithdrawal(wd *Withdrawal) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_withdrawals
		(id, node_id, provider, amount_eur, fee_eur, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET
		  status=EXCLUDED.status,
		  fee_eur=EXCLUDED.fee_eur`,
		wd.ID, wd.NodeID, wd.Provider, wd.AmountEur, wd.FeeEur,
		wd.Status, wd.CreatedAt)
	if err != nil {
		log.Printf("store.SaveWithdrawal: %v", err)
	}
}

// SaveKYC enregistre le statut KYC d'un vendeur (LAI-36).
func (s *Store) SaveKYC(k *KYC) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_kyc (node_id, status, checked_at)
		VALUES ($1,$2,$3)
		ON CONFLICT (node_id) DO UPDATE SET
		  status=EXCLUDED.status,
		  checked_at=EXCLUDED.checked_at`,
		k.NodeID, k.Status, k.CheckedAt)
	if err != nil {
		log.Printf("store.SaveKYC: %v", err)
	}
}

// SumChunkPayouts : total des gains des chunks termines (reconciliation
// LAI-34). 0 en mode memoire sans BDD (la reconciliation ne compare
// alors que le ledger a lui-meme, sans objet).
func (s *Store) SumChunkPayouts() float64 {
	if !s.enabled() {
		return 0
	}
	var sum sql.NullFloat64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(payout_eur), 0)
		FROM runtime_chunks WHERE status = 'done'`).Scan(&sum)
	if err != nil {
		log.Printf("store.SumChunkPayouts: %v", err)
		return 0
	}
	return sum.Float64
}

// LoadWallet recharge ledger, retraits et KYC au demarrage.
func (s *Store) LoadWallet(w *Wallet) {
	if !s.enabled() {
		return
	}
	rows, err := s.db.Query(`SELECT id, at, node_id, entry_type, amount_eur, ref
		FROM runtime_ledger ORDER BY id`)
	if err != nil {
		log.Printf("store.LoadWallet(ledger): %v", err)
	} else {
		for rows.Next() {
			var e LedgerEntry
			if rows.Scan(&e.Seq, &e.At, &e.NodeID, &e.EntryType,
				&e.AmountEur, &e.Ref) == nil {
				w.RestoreLedger(&e)
			}
		}
		rows.Close()
	}

	rows, err = s.db.Query(`SELECT id, node_id, provider, amount_eur,
		fee_eur, status, created_at FROM runtime_withdrawals ORDER BY created_at`)
	if err != nil {
		log.Printf("store.LoadWallet(withdrawals): %v", err)
	} else {
		for rows.Next() {
			var wd Withdrawal
			if rows.Scan(&wd.ID, &wd.NodeID, &wd.Provider, &wd.AmountEur,
				&wd.FeeEur, &wd.Status, &wd.CreatedAt) == nil {
				w.RestoreWithdrawal(&wd)
			}
		}
		rows.Close()
	}

	rows, err = s.db.Query(`SELECT node_id, status, checked_at FROM runtime_kyc`)
	if err != nil {
		log.Printf("store.LoadWallet(kyc): %v", err)
	} else {
		for rows.Next() {
			var k KYC
			var checked sql.NullTime
			if rows.Scan(&k.NodeID, &k.Status, &checked) == nil {
				if checked.Valid {
					k.CheckedAt = &checked.Time
				}
				w.RestoreKYC(&k)
			}
		}
		rows.Close()
	}
}

// SaveBillingEvent ajoute une ecriture au journal de billing (append-only).
func (s *Store) SaveBillingEvent(e *BillingEvent) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_billing_events
		(id, at, org_id, event_type, amount_eur, vat_eur, gpu_hours, ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO NOTHING`,
		e.Seq, e.At, e.OrgID, e.EventType, e.AmountEur,
		e.VatEur, e.GpuHours, e.Ref)
	if err != nil {
		log.Printf("store.SaveBillingEvent: %v", err)
	}
}

// SaveOrgBalance persiste le solde de credits d'une organisation.
func (s *Store) SaveOrgBalance(orgID string, balanceEur float64) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_billing_balances (org_id, balance_eur, updated_at)
		VALUES ($1,$2,now())
		ON CONFLICT (org_id) DO UPDATE SET
		  balance_eur=EXCLUDED.balance_eur,
		  updated_at=now()`,
		orgID, balanceEur)
	if err != nil {
		log.Printf("store.SaveOrgBalance: %v", err)
	}
}

// LoadBilling recharge le journal et les soldes de credits au demarrage.
func (s *Store) LoadBilling(b *Billing) {
	if !s.enabled() {
		return
	}
	rows, err := s.db.Query(`SELECT id, at, org_id, event_type,
		amount_eur, vat_eur, gpu_hours, ref
		FROM runtime_billing_events ORDER BY id`)
	if err != nil {
		log.Printf("store.LoadBilling(events): %v", err)
	} else {
		for rows.Next() {
			var e BillingEvent
			if rows.Scan(&e.Seq, &e.At, &e.OrgID, &e.EventType,
				&e.AmountEur, &e.VatEur, &e.GpuHours, &e.Ref) == nil {
				b.RestoreEvent(&e)
			}
		}
		rows.Close()
	}

	rows, err = s.db.Query(`SELECT org_id, balance_eur FROM runtime_billing_balances`)
	if err != nil {
		log.Printf("store.LoadBilling(balances): %v", err)
	} else {
		for rows.Next() {
			var orgID string
			var balance float64
			if rows.Scan(&orgID, &balance) == nil {
				b.RestoreBalance(orgID, balance)
			}
		}
		rows.Close()
	}
}

// LoadAll recharge l'etat au demarrage : prix courants, noeuds connus,
// organisations clientes, chunks non termines.
func (s *Store) LoadAll(reg *Registry, pricing *Pricing, chunker *Chunker, orgs *OrgStore) {
	if !s.enabled() {
		return
	}
	// 1. Prix courants : derniere valeur par categorie.
	rows, err := s.db.Query(`SELECT DISTINCT ON (category)
		category, eur_per_hour
		FROM runtime_price_history
		ORDER BY category, changed_at DESC`)
	if err != nil {
		log.Printf("store.LoadAll(prices): %v", err)
	} else {
		for rows.Next() {
			var cat string
			var eur float64
			if rows.Scan(&cat, &eur) == nil {
				pricing.Restore(cat, eur)
			}
		}
		rows.Close()
	}

	// 2. Noeuds connus (hors ligne jusqu'a re-enregistrement).
	rows, err = s.db.Query(`SELECT id, gpu_model, gpu_category,
		trust_score, status, zone,
		COALESCE(machine_fingerprint, ''),
		COALESCE(user_id, '') FROM runtime_nodes`)
	if err != nil {
		log.Printf("store.LoadAll(nodes): %v", err)
	} else {
		for rows.Next() {
			var id, model, cat, status, zone, fingerprint, userID string
			var trust float64
			if rows.Scan(&id, &model, &cat, &trust, &status, &zone, &fingerprint, &userID) == nil {
				reg.Restore(id, model, cat, trust, status, zone, fingerprint)
				if userID != "" {
					reg.BindSeller(id, userID)
				}
			}
		}
		rows.Close()
	}

	// 3. Organisations clientes.
	rows, err = s.db.Query(`SELECT id, name, api_key_hash FROM runtime_orgs`)
	if err != nil {
		log.Printf("store.LoadAll(orgs): %v", err)
	} else {
		for rows.Next() {
			var id, name, keyHash string
			if rows.Scan(&id, &name, &keyHash) == nil {
				orgs.Restore(id, name, keyHash)
			}
		}
		rows.Close()
	}

	// 4. Chunks non termines (running -> queued, re-claimables).
	rows, err = s.db.Query(`SELECT id, job_id, node_id, command, args,
		expected_output, timeout_secs, price_eur_per_hour,
		attempts, zone, assets, org_id
		FROM runtime_chunks WHERE status IN ('queued','running')`)
	if err != nil {
		log.Printf("store.LoadAll(chunks): %v", err)
	} else {
		for rows.Next() {
			var id, jobID, nodeID, cmd, argsJSON, output, orgID string
			var timeout, price float64
			var attempts int
			var zone sql.NullString
			var assetsJSON string
			if rows.Scan(&id, &jobID, &nodeID, &cmd, &argsJSON, &output,
				&timeout, &price, &attempts, &zone, &assetsJSON, &orgID) == nil {
				var args, assets []string
				if json.Unmarshal([]byte(argsJSON), &args) != nil {
					args = nil
				}
				if json.Unmarshal([]byte(assetsJSON), &assets) != nil {
					assets = nil
				}
				chunker.Restore(&Chunk{
					ID: id, JobID: jobID, NodeID: nodeID,
					OrgID:   orgID,
					Command: cmd, Args: args,
					ExpectedOutput:  output,
					TimeoutSecs:     uint64(timeout),
					Status:          "queued",
					PriceEurPerHour: price,
					Attempts:        attempts,
					Zone:            zone.String,
					AssetIDs:        assets,
				})
			}
		}
		rows.Close()
	}
	log.Printf("store: etat recharge depuis PostgreSQL (%s)",
		time.Now().Format("15:04:05"))
}

// ---- Sprint 15 : comptes utilisateurs (LAI-57) ----

// SaveUser enregistre un compte (idempotent). tokenHash : SHA-256 du
// jeton de noeud personnel (vide pour les clients). Sprint 15c : la
// preuve de consentement RGPD et l'epoque de revocation de sessions
// sont persistees des la creation.
func (s *Store) SaveUser(u *User, pwHash, tokenHash string) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_users
		(id, email, password_hash, role, display_name, org_id,
		 seller_token_hash, created_at, consent_at, sessions_before,
		 account_type, company_name, siret, vat_number,
		 company_address, siret_verified)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
		  email=EXCLUDED.email,
		  password_hash=EXCLUDED.password_hash,
		  display_name=EXCLUDED.display_name,
		  org_id=EXCLUDED.org_id,
		  seller_token_hash=EXCLUDED.seller_token_hash,
		  consent_at=EXCLUDED.consent_at,
		  sessions_before=EXCLUDED.sessions_before`,
		u.ID, u.Email, pwHash, u.Role, u.DisplayName, u.OrgID,
		tokenHash, u.CreatedAt,
		nullTime(u.ConsentAt), nullTime(u.SessionsBefore),
		u.AccountType, nullStr(u.CompanyName), nullStr(u.Siret),
		nullStr(u.VatNumber), nullStr(u.CompanyAddress),
		u.SiretVerified)
	if err != nil {
		log.Printf("store.SaveUser: %v", err)
	}
}

// SaveSellerToken met a jour le jeton de noeud d'un vendeur (rotation).
func (s *Store) SaveSellerToken(userID, tokenHash string) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`UPDATE runtime_users SET seller_token_hash=$2 WHERE id=$1`,
		userID, tokenHash)
	if err != nil {
		log.Printf("store.SaveSellerToken: %v", err)
	}
}

// SaveUserProfile met a jour le nom affiche (Sprint 15c).
func (s *Store) SaveUserProfile(id, name string) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`UPDATE runtime_users SET display_name=$2 WHERE id=$1`, id, name)
	if err != nil {
		log.Printf("store.SaveUserProfile: %v", err)
	}
}

// SaveUserPassword enregistre le nouveau hash de mot de passe et
// l'epoque de revocation : toutes les sessions emises avant sont
// invalidatees (Sprint 15c).
func (s *Store) SaveUserPassword(id, pwHash string, sessionsBefore time.Time) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`UPDATE runtime_users SET password_hash=$2, sessions_before=$3 WHERE id=$1`,
		id, pwHash, nullTime(sessionsBefore))
	if err != nil {
		log.Printf("store.SaveUserPassword: %v", err)
	}
}

// SaveUserDeleted anonymise un compte supprime (droit a l'effacement
// RGPD, Sprint 15c). Le mot de passe et le jeton sont efaces ;
// deleted_at marque la suppression.
func (s *Store) SaveUserDeleted(u *User) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`UPDATE runtime_users SET
		email=$2, display_name=$3, password_hash='', seller_token_hash='',
		org_id='', deleted_at=$4 WHERE id=$1`,
		u.ID, u.Email, u.DisplayName, nullTime(*u.DeletedAt))
	if err != nil {
		log.Printf("store.SaveUserDeleted: %v", err)
	}
}

// SaveUserCompany met a jour le statut particulier/societe et les
// infos societe (Sprint 15d, LAI-61).
func (s *Store) SaveUserCompany(u *User) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`UPDATE runtime_users SET
		account_type=$2, company_name=$3, siret=$4, vat_number=$5,
		company_address=$6, siret_verified=$7, siret_checked_at=$8
		WHERE id=$1`,
		u.ID, u.AccountType, nullStr(u.CompanyName), nullStr(u.Siret),
		nullStr(u.VatNumber), nullStr(u.CompanyAddress), u.SiretVerified,
		nullTimePtr(u.SiretCheckedAt))
	if err != nil {
		log.Printf("store.SaveUserCompany: %v", err)
	}
}

// LoadUsers recharge les comptes au demarrage. Sprint 15c : les
// comptes supprimes ne sont pas restaures (ils restent anonymises en
// base uniquement pour les ecritures comptables).
func (s *Store) LoadUsers(a *AccountStore) {
	if !s.enabled() {
		return
	}
	rows, err := s.db.Query(`SELECT id, email, password_hash, role,
		COALESCE(display_name, ''), COALESCE(org_id, ''),
		COALESCE(seller_token_hash, ''),
		COALESCE(consent_at, created_at),
		COALESCE(sessions_before, created_at),
		deleted_at,
		COALESCE(account_type, 'particulier'),
		COALESCE(company_name, ''), COALESCE(siret, ''),
		COALESCE(vat_number, ''), COALESCE(company_address, ''),
		siret_verified, siret_checked_at
		FROM runtime_users WHERE deleted_at IS NULL AND password_hash <> ''`)
	if err != nil {
		log.Printf("store.LoadUsers: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, email, pwHash, role, display, orgID, tokenHash string
		var accType, companyName, siret, vat, companyAddr string
		var consentAt, sessionsBefore time.Time
		var deletedAt, siretCheckedAt *time.Time
		var siretVerified bool
		if rows.Scan(&id, &email, &pwHash, &role, &display, &orgID, &tokenHash,
			&consentAt, &sessionsBefore, &deletedAt,
			&accType, &companyName, &siret, &vat, &companyAddr,
			&siretVerified, &siretCheckedAt) == nil {
			a.Restore(id, email, pwHash, role, display, orgID, tokenHash,
				consentAt, sessionsBefore, deletedAt,
				accType, companyName, siret, vat, companyAddr,
				siretVerified, siretCheckedAt)
		}
	}
}

// ------------------------------------------------------------------
// Sprint 24 : observabilite persistee
// ------------------------------------------------------------------

// SaveDashEvent ecrit une ligne du journal admin en base (append-only).
// Appele en goroutine par dashLog : les erreurs sont seulement loggees.
func (s *Store) SaveDashEvent(at time.Time, typ, msg string) {
	if !s.enabled() {
		return
	}
	if _, err := s.db.Exec(`INSERT INTO dash_events (at, type, msg)
		VALUES ($1,$2,$3)`, at, typ, msg); err != nil {
		log.Printf("store.SaveDashEvent: %v", err)
	}
}

// LoadDashEvents relit les n derniers evenements persistes (plus
// recents d'abord). Retourne nil si la persistance est inactive.
func (s *Store) LoadDashEvents(n int) []DashEvent {
	if !s.enabled() {
		return nil
	}
	rows, err := s.db.Query(`SELECT at, type, msg FROM dash_events
		ORDER BY at DESC, id DESC LIMIT $1`, n)
	if err != nil {
		log.Printf("store.LoadDashEvents: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]DashEvent, 0, n)
	for rows.Next() {
		var e DashEvent
		if rows.Scan(&e.At, &e.Type, &e.Msg) == nil {
			out = append(out, e)
		}
	}
	return out
}

// OrgJobRow : resume d'un job pour la console client, lu depuis
// l'historique persiste des chunks (runtime_chunks).
type OrgJobRow struct {
	JobID     string
	TaskCount int
	Statuses  map[string]int
	UpdatedAt time.Time
}

// LoadOrgJobs resume les jobs persistes d'une organisation, les plus
// recents d'abord. Retourne nil si la persistance est inactive.
func (s *Store) LoadOrgJobs(orgID string, limit int) []OrgJobRow {
	if !s.enabled() {
		return nil
	}
	q := `SELECT job_id, COUNT(*),
		COUNT(*) FILTER (WHERE status='queued'),
		COUNT(*) FILTER (WHERE status='running'),
		COUNT(*) FILTER (WHERE status='done'),
		COUNT(*) FILTER (WHERE status='failed'),
		COUNT(*) FILTER (WHERE status='canceled'),
		MAX(updated_at)
		FROM runtime_chunks WHERE org_id=$1
		GROUP BY job_id ORDER BY MAX(updated_at) DESC`
	args := []interface{}{orgID}
	if limit > 0 {
		q += ` LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		log.Printf("store.LoadOrgJobs: %v", err)
		return nil
	}
	defer rows.Close()
	out := []OrgJobRow{}
	for rows.Next() {
		var r OrgJobRow
		var queued, running, doneN, failedN, canceledN int
		if rows.Scan(&r.JobID, &r.TaskCount,
			&queued, &running, &doneN, &failedN, &canceledN, &r.UpdatedAt) == nil {
			r.Statuses = map[string]int{
				"queued": queued, "running": running, "done": doneN,
				"failed": failedN, "canceled": canceledN,
			}
			out = append(out, r)
		}
	}
	return out
}

// --- Sprint 26 : fichiers de resultat ---

// ResultRow : metadonnees d'un fichier de resultat pousse par un noeud.
type ResultRow struct {
	ChunkID  string
	JobID    string
	OrgID    string
	FileName string
	Size     int64
	SHA256   string
}

// SaveResult enregistre les metadonnees d'un fichier de resultat.
func (s *Store) SaveResult(r ResultRow) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_results
		(chunk_id, job_id, org_id, file_name, size_bytes, sha256, created_at)
		VALUES ($1,$2,$3,$4,$5,$6, now())
		ON CONFLICT (chunk_id) DO UPDATE SET
		  size_bytes = EXCLUDED.size_bytes,
		  sha256    = EXCLUDED.sha256,
		  file_name = EXCLUDED.file_name,
		  created_at = now()`,
		r.ChunkID, r.JobID, r.OrgID, r.FileName, r.Size, r.SHA256)
	if err != nil {
		log.Printf("store.SaveResult: %v", err)
	}
}

// LoadResult retrouve les metadonnees d'un fichier de resultat.
func (s *Store) LoadResult(chunkID string) *ResultRow {
	if !s.enabled() {
		return nil
	}
	var r ResultRow
	err := s.db.QueryRow(`SELECT chunk_id, job_id, org_id,
		COALESCE(file_name, 'result.bin'), size_bytes, COALESCE(sha256, '')
		FROM runtime_results WHERE chunk_id = $1`, chunkID).
		Scan(&r.ChunkID, &r.JobID, &r.OrgID, &r.FileName, &r.Size, &r.SHA256)
	if err != nil {
		return nil
	}
	return &r
}

// PurgeOldResults supprime les resultats plus vieux que la retention
// et retourne leurs chunk_id (pour effacer aussi les fichiers).
func (s *Store) PurgeOldResults(olderThan time.Duration) []string {
	if !s.enabled() {
		return nil
	}
	rows, err := s.db.Query(`DELETE FROM runtime_results
		WHERE created_at < now() - $1::interval
		RETURNING chunk_id`, fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
	if err != nil {
		log.Printf("store.PurgeOldResults: %v", err)
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ChunkFromDB retrouve un chunk termine uniquement connu de la base
// (apres un redemarrage du serveur). Retourne org + fichier attendu.
func (s *Store) ChunkFromDB(chunkID string) (orgID, status, outputName string) {
	if !s.enabled() {
		return "", "", ""
	}
	err := s.db.QueryRow(`SELECT COALESCE(org_id, ''), status,
		COALESCE(expected_output, '') FROM runtime_chunks WHERE id = $1`, chunkID).
		Scan(&orgID, &status, &outputName)
	if err != nil {
		return "", "", ""
	}
	return orgID, status, outputName
}
