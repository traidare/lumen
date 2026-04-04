#!/usr/bin/env -S 2>/dev/null=2>NUL sh
@goto batch 2>NUL;rm -f NUL

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
exec "${SCRIPT_DIR}/run.sh" "$@"

:batch
@echo off
set "SCRIPT_DIR=%~dp0"
call "%SCRIPT_DIR%run.bat" %*
exit /b %ERRORLEVEL%
