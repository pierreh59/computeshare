# Guide fournisseur de puissance - ComputeShare

Ce guide s'adresse aux particuliers (fournisseurs) qui louent la
puissance oisive de leur machine sur ComputeShare.

## 1. Principe

Vous installez l'application desktop ComputeShare. Quand vous
n'utilisez pas votre machine (pas de souris ni de clavier pendant
60 secondes), l'application :

1. signale votre disponibilite a la plateforme ;
2. recupere une tache d'un job client ;
3. l'execute dans un sandbox isole ;
4. remonte le resultat (verifie par empreinte SHA-256) ;
5. credite votre wallet vendeur, a la duree reelle d'execution.

Vous reprenez la main a tout moment : la tache en cours se termine,
aucune nouvelle n'est prise tant que vous utilisez la machine.

## 2. Installation

Prerequis : Windows 10/11, une carte GPU NVIDIA, ~2 Go d'espace
disque.

1. Recuperer le jeton d'enrolement fourni pendant la beta (variable
   `COMPUTERSHARE_NODE_TOKEN`).
2. Lancer l'application, renseigner l'adresse de la plateforme
   (gateway) puis "Demarrer le noeud".
3. C'est tout. L'application tourne en arriere-plan.

## 3. Securite : ce que la plateforme peut (et ne peut pas) faire

L'execution est confinee dans un sandbox :

- uniquement les programmes autorises (allowlist) ;
- pas d'acces reseau pendant l'execution ;
- repertoire de travail temporaire, efface ensuite ;
- pas d'acces a vos fichiers personnels ;
- timeout d'execution maximal.

Vous pouvez arreter le noeud a tout moment, et consulter le journal
des taches executees dans l'application.

## 4. Vos gains

- Votre machine est classee par categorie (consumer / prosumer /
  high_end) selon son materiel : le tarif horaire depend de cette
  categorie.
- Gains credites a la duree reelle, visibles en temps reel dans
  l'application (compteur "Gains cumules") et dans votre wallet.
- Retraits possibles vers un compte bancaire apres verification
  d'identite (KYC) - obligatoire pour tout paiement reel.

## 5. Bonnes pratiques

- Laissez la machine branchee et connectee pour maximiser les
  creneaux de disponibilite.
- Les jobs de calcul (encodage, rendu, simulation) utilisent le
  GPU et le CPU : une bonne ventilation prolonge la duree de vie
  du materiel.
- En cas de probleme : le journal de l'application indique chaque
  tache et son statut.

## 6. Conditions

La participation suppose l'acceptation des CGU (disponibles avant
tout paiement reel). Les gains sont des revenus a declarer selon
votre situation fiscale : ComputeShare fournit un releve annuel
des montants credites.
