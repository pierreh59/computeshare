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

; Sprint 32 (0.7.11) : correctif "Failed to kill ComputeShare" + app qui
; ne redemarre pas apres la mise a jour. Cause : depuis Tauri CLI 2.12,
; l'installateur utilise le Windows Restart Manager, qui echoue si l'exe
; est encore en cours de fermeture (issue tauri #16277). Correctif eprouve
; du projet Monoleaf (PR #101) : attendre que l'exe instale soit de
; nouveau ouvrable en ecriture (un exe en execution est verrouille)
; AVANT de laisser le check du template tourner. L'installateur lance par
; l'updater redemarre ensuite l'app tout seul (/R) : fixer le kill fixe
; aussi le redemarrage. NB : l'exe instale s'appelle ${MAINBINARYNAME}.exe
; (ComputeShare), PAS computeshare-desktop.exe (nom du binaire cargo) :
; l'ancien taskkill ne tuait donc jamais le bon processus.
!macro NSIS_HOOK_PREINSTALL
  ; Fermeture best-effort (install manuelle avec l'appli ouverte).
  nsExec::ExecToLog 'taskkill /F /T /IM ${MAINBINARYNAME}.exe'
  ; Attente que le fichier ne soit plus verrouille (max ~20 s).
  StrCpy $R9 0
  IfFileExists "$INSTDIR\${MAINBINARYNAME}.exe" 0 cs_wait_done
  cs_wait_loop:
    ClearErrors
    FileOpen $R8 "$INSTDIR\${MAINBINARYNAME}.exe" a
    IfErrors cs_wait_busy cs_wait_free
  cs_wait_busy:
    IntOp $R9 $R9 + 1
    IntCmp $R9 80 cs_wait_done cs_wait_retry cs_wait_done
  cs_wait_retry:
    Sleep 250
    Goto cs_wait_loop
  cs_wait_free:
    FileClose $R8
  cs_wait_done:
!macroend
