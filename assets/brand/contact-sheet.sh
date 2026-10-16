#!/usr/bin/env bash
# Gera contact-sheet.png — a folha de contato da marca, para conferir num olhar
# se os SVGs ainda batem entre si e com o protótipo no Penpot.
#
# Rode depois de mexer em qualquer SVG deste diretório:
#   ./contact-sheet.sh
#
# Requer: rsvg-convert (librsvg) e magick (ImageMagick 7).
set -euo pipefail
cd "$(dirname "$0")"

command -v rsvg-convert >/dev/null || { echo "falta rsvg-convert (librsvg)" >&2; exit 1; }
command -v magick       >/dev/null || { echo "falta magick (ImageMagick 7)" >&2; exit 1; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

BG='#16213E'; CLARO='#F5F5F5'
TXT='#FFFFFF'; MUTED='#CFD3E0'; FRACO='#8B90A8'; LINHA='#2C3A5E'

FONTES=$(magick -list font 2>/dev/null || true)
if   [[ "$FONTES" == *"Font: Source-Sans-3-Regular"* ]]; then FONTE=Source-Sans-3-Regular
elif [[ "$FONTES" == *"Font: DejaVu-Sans"*           ]]; then FONTE=DejaVu-Sans
else FONTE=Helvetica; fi

# render <svg> <px> <nome>
render() { rsvg-convert -w "$2" -h "$2" "$1" -o "$TMP/$3.png"; }
# zoom <nome> <fator> — amplia o PNG já rasterizado, sem suavizar: mostra o pixel real
zoom() { magick "$TMP/$1.png" -filter point -resize "$(( $2 * 100 ))%" "$TMP/$1-x$2.png"; }

for s in 120 64 48 40 32; do render logo.svg          "$s" "c$s"; done
for s in 24 16;             do render logo-compact.svg "$s" "m$s"; done
for s in 64 32;             do render favicon.svg      "$s" "f$s"; done
render favicon-min.svg 16 fm16

# o mono herda a cor do contexto (currentColor): renderizado nas duas pontas de uso
printf 'svg { color: %s; }' '#FFFFFF' > "$TMP/claro.css"
printf 'svg { color: %s; }' '#1A1A2E' > "$TMP/escuro.css"
rsvg-convert -s "$TMP/claro.css"  -w 64 -h 64 logo-mono.svg -o "$TMP/mono-branco.png"
rsvg-convert -s "$TMP/escuro.css" -w 64 -h 64 logo-mono.svg -o "$TMP/mono-tinta.png"
zoom c32 4; zoom m24 4; zoom m16 4; zoom f32 4; zoom fm16 4

# sobre fundo claro é o que prova que os LEDs são furos, não pontos brancos
magick -size 88x80 "xc:$CLARO" "$TMP/mono-tinta.png" -geometry +12+8 -composite "$TMP/mono-claro.png"

# -- a folha, montada por blocos --------------------------------------------
args=( -size 940x944 "xc:$BG" -font "$FONTE" )
texto()  { args+=( -fill "$1" -pointsize "$2" -annotate "+$3+$4" "$5" ); }
imagem() { args+=( "$TMP/$1.png" -geometry "+$2+$3" -composite ); }

texto "$TXT"   26 36 48 'pushObserver — marca'
texto "$MUTED" 13 36 74 'Todas as peças saem do mesmo grid 120. Completo de 32 px para cima, mínimo de 24 px para baixo.'
args+=( -fill "$LINHA" -draw 'rectangle 36,96 904,97' )

texto "$FRACO" 12 36 126 'COMPLETO — logo.svg'
imagem c120 36 148; imagem c64 192 204; imagem c48 282 220; imagem c40 356 228; imagem c32 420 236
texto "$MUTED" 11 36 288 '120'; texto "$MUTED" 11 192 288 '64'; texto "$MUTED" 11 282 288 '48'
texto "$MUTED" 11 356 288 '40'; texto "$MUTED" 11 420 288 '32'

texto "$FRACO" 12 36 328 'MÍNIMO — logo-compact.svg'
imagem m24 36 346; imagem m16 84 354
texto "$MUTED" 11 36 388 '24'; texto "$MUTED" 11 84 388 '16'

texto "$FRACO" 12 36 430 'FAVICON (tile) — favicon.svg · favicon-min.svg'
imagem f64 36 440; imagem f32 124 472; imagem fm16 180 488
texto "$MUTED" 11 36 522 '64'; texto "$MUTED" 11 124 522 '32'; texto "$MUTED" 11 176 522 '16 min'

texto "$FRACO" 12 36 564 'MONO — logo-mono.svg (herda a cor; os LEDs do rack são furos)'
imagem mono-branco 36 578; imagem mono-claro 124 570
texto "$MUTED" 11 36 662 'color: #FFFFFF'; texto "$MUTED" 11 124 662 'color: #1A1A2E'

texto "$FRACO" 12 36 702 'INSPEÇÃO — o pixel real ampliado 4×'
imagem c32-x4 36 716; imagem m24-x4 196 748; imagem m16-x4 324 780
imagem f32-x4 420 716; imagem fm16-x4 580 780
texto "$MUTED" 11 36 862 '32 completo'; texto "$MUTED" 11 196 862 '24 mínimo'
texto "$MUTED" 11 324 862 '16 mínimo'; texto "$MUTED" 11 420 862 '32 favicon'
texto "$MUTED" 11 580 862 '16 favicon-min'

texto "$FRACO" 11 36 912 'Abaixo de 32 px as três barras do rack ficam com menos de 1 px de vão e se fundem — daí a versão mínima. Gerado por assets/brand/contact-sheet.sh'

# paleta de 128 cores: 24 KB em vez de 143 — o desenho é chapado, não perde nada
magick "${args[@]}" -strip -depth 8 +dither -colors 128 contact-sheet.png
echo "gerado: $(pwd)/contact-sheet.png"
