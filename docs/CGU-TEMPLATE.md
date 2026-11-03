# ComputeShare — Templates CGU (brouillon pour validation juridique)

> ⚠️ **Document de travail.** Ces templates ont ete rediges pour cadrer
> le perimetre juridique de ComputeShare pendant la beta fermee.
> Ils NE SONT PAS utilisables en l'etat : ils doivent etre relus,
> completes et valides par un professionnel du droit avant toute
> ouverture publique ou tout paiement reel (ticket LAI-16).

## 1. CGU Vendeurs (fournisseurs de puissance)

1. **Objet** : le vendeur met a disposition la puissance de calcul de
   sa machine personnelle (GPU/CPU) pendant ses periodes d'inactivite,
   via le client desktop ComputeShare.
2. **Enrolement et responsabilites** :
   - un seul compte vendeur par machine physique (empreinte machine) ;
   - le vendeur declare une machine dont il est proprietaire ou qu'il
     est autorise a utiliser ;
   - le vendeur s'engage a ne pas executer de contournement du sandbox.
3. **Remuneration** :
   - gains credites sur un wallet exprime en euros, calcul au tarif
     courant x duree reelle d'execution (jamais retroactif) ;
   - retraits a partir de 5 EUR, via PayPal ou gift cards, frais
     affiches avant confirmation ;
   - KYC obligatoire avant tout premier retrait (blocage pendant le
     controle) ;
   - ⚠️ A TRAITER PAR L'AVOCAT : qualification fiscale des gains
     (BIC declare par le vendeur ? plafonds ?) et statut de la
     plateforme (intermediaire vs donneur d'ordre).
4. **Anti-fraude et confiance** : trust score, alertes Sybil et
   execution ralentie ; la plateforme peut suspendre un compte
   vendeur suspect (retraits bloques, investigation).
5. **Donnees personnelles (RGPD)** :
   - traitees : identifiant de machine, empreinte machine (nom de
     machine + modele CPU), volumes d'execution, gains ;
   - finalite : securite anti-fraude et facturation ;
   - base legale : execution du contrat + interet legitime (securite) ;
   - duree de conservation : ⚠️ A FIXER (prop. 24 mois + obligations
     comptables) ;
   - droit d'acces/rectification/effacement : formulaire a prevoir.
6. **Executions sur la machine du vendeur** :
   - les jobs s'executent dans un sandbox (restrictions reseau et
     systeme) ;
   - le vendeur reste responsable de la conformite des contenus
     traites sur sa machine.

## 2. CGU Clients B2B

1. **Objet** : le client commande de la puissance de calcul
   (decoupage en chunks, execution distribuee).
2. **Facturation** : prepayment en credits (EUR), facturation a la
   GPU-heure reelle (tarif gele par chunk a la livraison), TVA
   applicable selon le pays du client (OSS ; autoliquidation pour les
   entreprises UE : ⚠️ A VALIDER).
3. **Engagements du client** : contenu conforme a la loi, pas de
   charges illegales (minage illicite, cracking, contenu interdit),
   respect des quotas.
4. **Niveaux de service** : la plateforme est en beta : pas de SLA
   contractuel ; re-scheduling automatique des chunks ; remboursement
   des credits en cas d'echec definitif d'execution.
5. **Acceptation** : ⚠️ REMPLIR : forme de l'acceptation electronique,
     edition de version, droit applicable (France), tribunal
     competemment designe, mediation de la consommation (non
     applicable B2B mais a verifier).

## 3. Checklist avant ouverture publique (a rouvrir : LAI-16)

- [ ] Validation des CGU par un professionnel du droit (EI Pierre Deg)
- [ ] Politique de confidentialite RGPD publiee + registre de traitement
- [ ] Mentions legales (editeur EI, hebergeur)
- [ ] Validation fiscale : TVA OSS, qualification des gains vendeurs
- [ ] CGV et CGU distinctes ? (B2C vendeurs vs B2B clients)
- [ ] Procedure de retrait de contenu illicite (notice et retrait)
- [ ] Conservation des logs anti-fraude (duree + base legale)
- [ ] Conditions de retrait / fermeture de compte vendeur
