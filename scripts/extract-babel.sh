#!/bin/zsh
set -u
cd "$(dirname "$0")/.."
CACHE="$HOME/.yarn/berry/cache"
mkdir -p node_modules
SPECS="
@babel/core:7.29.7
@babel/code-frame:7.29.7
@babel/generator:7.29.7
@babel/helper-compilation-targets:7.29.7
@babel/helper-module-transforms:7.29.7
@babel/helpers:7.29.7
@babel/parser:7.29.7
@babel/template:7.29.7
@babel/traverse:7.29.7
@babel/types:7.29.7
@babel/helper-module-imports:7.29.7
@babel/helper-plugin-utils:7.29.7
@babel/helper-validator-identifier:7.29.7
@babel/helper-validator-option:7.29.7
@babel/helper-string-parser:7.29.7
@babel/helper-globals:7.29.7
@babel/helper-annotate-as-pure:7.29.7
@babel/helper-create-class-features-plugin:7.29.7
@babel/helper-member-expression-to-functions:7.29.7
@babel/helper-optimise-call-expression:7.29.7
@babel/helper-replace-supers:7.29.7
@babel/helper-skip-transparent-expression-wrappers:7.29.7
@babel/plugin-syntax-typescript:7.29.7
@babel/plugin-transform-typescript:7.29.7
@babel/plugin-transform-modules-commonjs:7.29.7
@babel/preset-typescript:7.29.7
debug:4.4.3
ms:2.1.3
gensync:1.0.0-beta.2
json5:2.2.3
semver:6.3.1
browserslist:4.28.2
caniuse-lite:1.0.30001787
electron-to-chromium:1.5.334
node-releases:2.0.37
escalade:3.2.0
picocolors:1.1.1
convert-source-map:2.0.0
jsesc:3.1.0
lru-cache:6.0.0
yallist:4.0.0
update-browserslist-db:1.2.3
baseline-browser-mapping:2.10.16
picomatch:4.0.4
"
i=0
for spec in ${(f)SPECS}; do
  [ -z "$spec" ] && continue
  name="${spec%%:*}"
  ver="${spec##*:}"
  flat=$(echo "$name" | tr '/' '-')
  zip=$(ls "$CACHE"/${flat}-npm-${ver}-*.zip 2>/dev/null | head -1)
  if [ -z "$zip" ]; then echo "MISSING $name@$ver"; continue; fi
  dest="node_modules/$name"
  [ -d "$dest" ] && continue
  i=$((i+1))
  tmp="/tmp/zapex_$$_$i"
  mkdir -p "$tmp" "$dest"
  unzip -qq -o "$zip" -d "$tmp"
  inner="$tmp/node_modules/$name"
  if [ -d "$inner" ]; then
    cp -R "$inner/." "$dest/"
  else
    echo "BADZIP $name"
  fi
done
echo "done"
ls node_modules | wc -l
