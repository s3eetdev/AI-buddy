param([string]$Python = '')
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path $PSScriptRoot -Parent
if (-not $Python) { $Python = Join-Path $repoRoot '.venv/Scripts/python.exe' }
Push-Location $repoRoot
try {
    if (-not (Test-Path -LiteralPath $Python)) { throw 'Create .venv and install requirements.txt and requirements-dev.txt first.' }
    New-Item -ItemType Directory -Force artifacts/coverage | Out-Null
    & $Python -m pytest tests -q -p no:cacheprovider --basetemp=artifacts/tmp-coverage --cov=navigator --cov=scripts --cov=main --cov-report=term-missing --cov-report=xml:artifacts/coverage.xml --cov-report=json:artifacts/coverage/python.json --cov-fail-under=81
    if ($LASTEXITCODE -ne 0) { throw 'Python tests/coverage failed' }
    & $Python -m ruff check navigator tests scripts main.py tools/coverage_summary.py
    if ($LASTEXITCODE -ne 0) { throw 'Python lint failed' }
    Push-Location frontend
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        npm run test:coverage
        if ($LASTEXITCODE -ne 0) { throw 'Frontend tests/coverage failed' }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
        npm run lint
        if ($LASTEXITCODE -ne 0) { throw 'Frontend lint failed' }
    } finally { Pop-Location }
    docker compose -f compose.yaml -f compose.coverage.yaml run --build --rm backend-tests
    if ($LASTEXITCODE -ne 0) { throw 'Go tests/coverage/vet failed' }
    & $Python tools/coverage_summary.py
    if ($LASTEXITCODE -ne 0) { throw 'Combined coverage gate failed' }
} finally { Pop-Location }
