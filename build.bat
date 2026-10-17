@echo off
REM Thin wrapper around build.ps1. Kept so double-clicking works on a machine where
REM PowerShell execution policy blocks .ps1 by default.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" %*
exit /b %ERRORLEVEL%