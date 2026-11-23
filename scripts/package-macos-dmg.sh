#!/usr/bin/env bash
set -euo pipefail

signing_variables=(
  MACOS_CERTIFICATE_P12_BASE64
  MACOS_CERTIFICATE_PASSWORD
  MACOS_NOTARY_KEY_P8_BASE64
  MACOS_NOTARY_KEY_ID
  MACOS_NOTARY_ISSUER_ID
)
# Keep signing inputs in this shell, out of encoder and build subprocesses.
export -n "${signing_variables[@]}"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

cd -- "$(dirname -- "$0")/.."
root="$(pwd -P)"
[[ "$(uname -s)/$(uname -m)" == Darwin/arm64 ]] || fail '打包需要 Apple 芯片 Mac。'

version="$(plutil -extract version raw -o - mygo.json)"
name="$(plutil -extract name raw -o - mygo.json)"
slug="$(plutil -extract slug raw -o - mygo.json)"
minimum="$(plutil -extract macos.minimumSystemVersion raw -o - mygo.json)"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'mygo.json 的 version 必须为 X.Y.Z。'
if [[ "${GITHUB_REF_TYPE:-}" == tag && "${GITHUB_REF_NAME:-}" != "v$version" ]]; then
  fail "版本标签必须与 mygo.json 中的版本一致，预期为 v$version。"
fi

mkdir -p "$root/build/macos-dmg"
out="$(mktemp -d "$root/build/macos-dmg/run.XXXXXX")"
printf '打包目录 %s\n' "$out"

mountpoint=""
signing_dir=""
keychain_created=false
keychains_changed=false
original_keychains=()
identity=-
notarized=false

cleanup() {
  if [[ -n "$mountpoint" ]]; then
    hdiutil detach "$mountpoint" >/dev/null 2>&1 || hdiutil detach -force "$mountpoint" >/dev/null 2>&1 || true
  fi
  if [[ "$keychains_changed" == true ]]; then
    security list-keychains -d user -s ${original_keychains[@]+"${original_keychains[@]}"} || true
  fi
  if [[ "$keychain_created" == true ]]; then
    security delete-keychain "$keychain" || true
  fi
  if [[ -n "$signing_dir" ]]; then
    rm -f -- "$signing_dir/certificate.p12" "$signing_dir/notary-key.p8"
    rmdir -- "$signing_dir" || true
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

prepare_signing() {
  local missing=()
  local variable password entry keychain_list
  for variable in "${signing_variables[@]}"; do
    if [[ -z "${!variable:-}" ]]; then
      missing+=("$variable")
    fi
  done
  if (( ${#missing[@]} == ${#signing_variables[@]} )); then
    printf '未配置发布签名。此标签将使用临时签名构建并校验。\n'
    return
  fi
  if (( ${#missing[@]} > 0 )); then
    fail "发布签名缺少以下 Secrets：${missing[*]}"
  fi

  signing_dir="$(mktemp -d "${RUNNER_TEMP:?}/z-pic-signing.XXXXXX")"
  keychain="$signing_dir/signing.keychain-db"
  password="$(openssl rand -hex 32)"
  printf '%s' "$MACOS_CERTIFICATE_P12_BASE64" | base64 -D > "$signing_dir/certificate.p12"
  printf '%s' "$MACOS_NOTARY_KEY_P8_BASE64" | base64 -D > "$signing_dir/notary-key.p8"
  unset MACOS_CERTIFICATE_P12_BASE64 MACOS_NOTARY_KEY_P8_BASE64

  security create-keychain -p "$password" "$keychain"
  keychain_created=true
  security set-keychain-settings -lut 3600 "$keychain"
  security unlock-keychain -p "$password" "$keychain"
  security import "$signing_dir/certificate.p12" -f pkcs12 -k "$keychain" -P "$MACOS_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
  unset MACOS_CERTIFICATE_PASSWORD
  rm -f -- "$signing_dir/certificate.p12"
  security set-key-partition-list -S apple-tool:,apple: -s -k "$password" "$keychain" >/dev/null

  identity="$(security find-identity -v -p codesigning "$keychain" | awk '/"Developer ID Application:/ {print $2}')"
  [[ "$identity" =~ ^[[:xdigit:]]{40}$ ]] || fail '证书文件必须包含一个有效的 Developer ID Application 签名身份。'
  keychain_list="$(security list-keychains -d user)"
  while IFS= read -r entry; do
    if [[ "$entry" =~ \"(.*)\" ]]; then
      original_keychains+=("${BASH_REMATCH[1]}")
    fi
  done <<< "$keychain_list"
  keychains_changed=true
  security list-keychains -d user -s "$keychain" ${original_keychains[@]+"${original_keychains[@]}"}
}

notarize_dmg() {
  local result status
  codesign --verify --strict "$dmg"
  if ! result="$(xcrun notarytool submit "$dmg" --key "$signing_dir/notary-key.p8" \
    --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" \
    --wait --timeout 30m --output-format json)"; then
    printf '%s\n' "$result" >&2
    fail 'Apple 公证提交失败。'
  fi
  status="$(printf '%s' "$result" | plutil -extract status raw -o - -)"
  if [[ "$status" != Accepted ]]; then
    printf '%s\n' "$result" >&2
    fail 'Apple 公证未通过。'
  fi
  xcrun stapler staple "$dmg"
  xcrun stapler validate "$dmg"
  codesign --verify --strict "$dmg"
  spctl --assess --type open --context context:primary-signature --verbose "$dmg"
}

for tool in png webp jpeg avif; do
  bash "scripts/prepare-${tool}tools.sh" --verify
done

if [[ "${GITHUB_EVENT_NAME:-}" == push && "${GITHUB_REF_TYPE:-}" == tag ]]; then
  prepare_signing
fi

go tool mygo build -platform darwin/arm64 -o "$out" -sign "$identity" -skip-notarize

dmg_name="$slug-$version-arm64.dmg"
dmg="$out/darwin-arm64/$dmg_name"
checksum="$dmg.sha256"
if [[ "$identity" != - ]]; then
  notarize_dmg
fi
hdiutil verify "$dmg"

mountpoint="$out/mounted"
hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$mountpoint" "$dmg"
app="$mountpoint/$name.app"
plist="$app/Contents/Info.plist"
resources="$app/Contents/Resources"
executable="$(plutil -extract CFBundleExecutable raw -o - "$plist")"
binary="$app/Contents/MacOS/$executable"

[[ "$(readlink "$mountpoint/Applications")" == /Applications ]] || fail 'DMG 缺少「应用程序」链接。'
[[ "$(plutil -extract CFBundleShortVersionString raw -o - "$plist")" == "$version" ]] || fail '应用版本与打包版本不一致。'
[[ "$(lipo -archs "$binary")" == arm64 ]] || fail '应用架构应为 arm64。'
[[ "$(plutil -extract LSMinimumSystemVersion raw -o - "$plist")" == "$minimum" ]] || fail '应用声明的最低系统版本与打包配置不一致。'
[[ "$(xcrun vtool -show-build "$binary" | awk '$1 == "minos" {print $2}')" == "$minimum" ]] || fail '主程序要求的最低系统版本与打包配置不一致。'

codesign --verify --deep --strict "$app"
cmp "$root/THIRD_PARTY_NOTICES.md" "$resources/THIRD_PARTY_NOTICES.md"
diff -r "$root/licenses" "$resources/licenses"
[[ "$("$binary" version)" == "$slug $version (darwin/arm64)" ]] || fail 'DMG 内应用的命令行启动校验失败。'
if [[ "$identity" != - ]]; then
  spctl --assess --type execute --verbose "$app"
  notarized=true
fi
hdiutil detach "$mountpoint"
mountpoint=""

(
  cd -- "$out/darwin-arm64"
  shasum -a 256 "$dmg_name" > "$dmg_name.sha256"
  shasum -a 256 -c "$dmg_name.sha256"
)
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'dmg=%s\nchecksum=%s\nnotarized=%s\n' "$dmg" "$checksum" "$notarized" >> "$GITHUB_OUTPUT"
fi
printf 'DMG %s\nSHA-256 %s\n' "$dmg" "$checksum"
