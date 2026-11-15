#!/bin/sh
# ScopeWeaver quiet-default shim. Invoked through a name symlink/copy in a PATH
# directory ordered before the real toolchain: append the scanner's quiet flags
# only when the caller did not pass them, then exec the real binary so its exit
# code, signals and stdio pass through untouched.
name=${0##*/}
self_dir=${0%/*}

real=
old_ifs=$IFS
IFS=:
for dir in $PATH; do
	[ -n "$dir" ] || dir=.
	[ "$dir" = "$self_dir" ] && continue
	if [ -x "$dir/$name" ]; then
		real=$dir/$name
		break
	fi
done
IFS=$old_ifs

if [ -z "$real" ]; then
	printf 'quiet shim: %s not found on PATH\n' "$name" >&2
	exit 127
fi

while read -r flag value detect; do
	[ -n "$flag" ] || continue
	[ "$value" = "-" ] && value=
	[ -n "$detect" ] || detect=$flag
	present=
	for arg in "$@"; do
		case $detect in
		--*)
			case $arg in
			"$detect" | "$detect"=*) present=1 ;;
			esac
			;;
		*)
			case $arg in
			"$detect"* | "$detect"=* | -"$detect"* | -"$detect"=*) present=1 ;;
			esac
			;;
		esac
		[ -n "$present" ] && break
	done
	[ -n "$present" ] && continue
	if [ -n "$value" ]; then
		set -- "$@" "$flag" "$value"
	else
		set -- "$@" "$flag"
	fi
done <<'SHIM_DEFAULTS'
@DEFAULTS@
SHIM_DEFAULTS

exec "$real" "$@"
