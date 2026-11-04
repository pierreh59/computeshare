# App desktop ComputeShare (Tauri 2) - fournisseur de puissance GPU

Sprint 16 (LAI-62) : experience "zero configuration" pour le
fournisseur :

1. il cree son compte fournisseur sur https://computeshare.fr/signup ;
2. il telecharge l'installateur Windows depuis les GitHub Releases ;
3. il installe (currentUser, pas de droits admin) et se connecte avec
   son e-mail + mot de passe.

Tout le reste est automatique :
- le jeton de noeud personnel est recupere via le compte (rotation
  POST /auth/seller-token) et persiste dans %APPDATA%\ComputeShare ;
- le node_id est stable (les gains ne repartent pas de zero) ;
- le noeud demarre tout seul au lancement de l'app ;
- case a cocher "Lancer au demarrage de Windows" (desactivable) ;
- mises a jour automatiques signees (plugin updater + latest.json) ;
- reseau : HTTPS sortant uniquement vers api.computeshare.fr, aucun
  port a ouvrir, aucun reglage firewall.

Build de l'installateur : workflow "Desktop release" (manuel) - voir
.github/workflows/desktop-release.yml. Preconditions : secrets
TAURI_SIGNING_PRIVATE_KEY et TAURI_SIGNING_PRIVATE_KEY_PASSWORD.

Tickets historiques : LAI-18/19/20/21/22/23/53.
