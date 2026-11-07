; ComputeShare - hooks de l'installateur NSIS (Tauri v2).
; Sprint 25b (0.6.1) : parcours fournisseur sans friction. Si Docker
; Desktop n'est pas installe, on le lance en arriere-plan via winget
; (silencieux, ~500 Mo). Les jobs conteneur (et GPU) en ont besoin ;
; l'app le detecte toute seule (wsl2_ready / gpu_ready au register).
;
; L'installateur lui-meme reste en mode "utilisateur courant" (pas de
; mot de passe admin pour installer l'app). L'installation de Docker
; Desktop est une installation machine : winget demandera une seule
; confirmation Windows (UAC), puis tout est silencieux.
;
; Commande PowerShell encodee (Base64 UTF-16LE) pour eviter tout
; probleme de guillemets dans NSIS :
;   Start-Process winget -ArgumentList 'install','-e','--id',
;     'Docker.DockerDesktop','--silent','--accept-package-agreements',
;     '--accept-source-agreements' -Verb RunAs

!macro NSIS_HOOK_POSTINSTALL
  ; Docker Desktop deja present ? (chemin d'installation standard)
  IfFileExists "$PROGRAMFILES64\Docker\Docker\Docker Desktop.exe" skipDockerInstall 0
    MessageBox MB_OK|MB_ICONINFORMATION "Docker Desktop n'est pas installe sur ce PC.$\r$\n$\r$\nIl est necessaire pour les jobs conteneur (les mieux payes, dont les jobs GPU).$\r$\nL'installation se lance maintenant en arriere-plan (quelques minutes, environ 500 Mo) : acceptez la demande de confirmation Windows si elle apparait.$\r$\n$\r$\nVous pouvez terminer l'installation de ComputeShare sans attendre."
    nsExec::ExecToLog 'powershell -NoProfile -WindowStyle Hidden -EncodedCommand UwB0AGEAcgB0AC0AUAByAG8AYwBlAHMAcwAgAHcAaQBuAGcAZQB0ACAALQBBAHIAZwB1AG0AZQBuAHQATABpAHMAdAAgACcAaQBuAHMAdABhAGwAbAAnACwAJwAtAGUAJwAsACcALQAtAGkAZAAnACwAJwBEAG8AYwBrAGUAcgAuAEQAbwBjAGsAZQByAEQAZQBzAGsAdABvAHAAJwAsACcALQAtAHMAaQBsAGUAbgB0ACcALAAnAC0AYQBjAGMAZQBwAHQALQBwAGEAYwBrAGEAZwBlAG0AZQBuAHQAcwAnACwAJwAtAC0AYQBjAGMAZQBwAHQALQAtAHMAbwB1AHIAYwBlAC0AYQBnAHIAZQBlAG0AZQBuAHQAcwAnACAALQBWAGUAcgBiACAAUgB1AG4AQQBzAA=='
  skipDockerInstall:
!macroend
