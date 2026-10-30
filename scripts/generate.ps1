$ErrorActionPreference = 'Stop'
$savedGoBin = $env:GOBIN
Push-Location (Join-Path $PSScriptRoot '..')
try {
    $protoc = Get-Command protoc -ErrorAction SilentlyContinue
    if ($null -eq $protoc) {
        $localProtoc = Join-Path (Get-Location) '.tools/protoc/bin/protoc.exe'
        if (-not (Test-Path -LiteralPath $localProtoc)) { throw 'Install protoc 27.2 and add it to PATH.' }
        $protocPath = $localProtoc
        $includePath = Join-Path (Get-Location) '.tools/protoc/include'
    } else {
        $protocPath = $protoc.Source
        $includePath = Join-Path (Split-Path (Split-Path $protocPath)) 'include'
    }
    $env:GOBIN = Join-Path (Get-Location) '.tools/bin'
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
    if ($LASTEXITCODE -ne 0) { throw 'protoc-gen-go installation failed' }
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0
    if ($LASTEXITCODE -ne 0) { throw 'protoc-gen-go-grpc installation failed' }
    $savedPath = $env:PATH
    $env:PATH = $env:GOBIN + ';' + $env:PATH
    & $protocPath -I proto -I $includePath --go_out=. --go_opt=module=example.com/productcatalog --go-grpc_out=. --go-grpc_opt=module=example.com/productcatalog proto/product.proto
    if ($LASTEXITCODE -ne 0) { throw 'Code generation failed' }
} finally {
    if ($null -ne $savedPath) { $env:PATH = $savedPath }
    $env:GOBIN = $savedGoBin
    Pop-Location
}
