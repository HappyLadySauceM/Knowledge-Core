#!/usr/bin/env bash
set -euo pipefail

: "${IMAGE_MAP_FILE:?IMAGE_MAP_FILE is required}"
: "${VERSION_FILE:?VERSION_FILE is required}"
: "${DOCKER_CONFIG:?DOCKER_CONFIG is required}"

version="$(<"$VERSION_FILE")"
registry="${IMAGE_REGISTRY:-harbor.happyladysauce.local}"
config_file="${DOCKER_CONFIG}/config.json"
auth="$(jq -er --arg registry "$registry" '.auths[$registry].auth' "$config_file" | base64 -d)"
accept='application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json'

work="$(mktemp -d)"
trap 'case "$work" in /tmp/*) rm -rf -- "$work" ;; esac' EXIT

for service in gateway identity knowledge collaboration; do
    source="$(sed -n "s|^knowledge-core-${service}=||p" "$IMAGE_MAP_FILE")"
    expected="${source##*@}"
    repository="knowledge-core/${service}"
    source_headers="$work/${service}-source.headers"
    manifest="$work/${service}.manifest"
    curl --fail --silent --show-error \
        --user "$auth" \
        --dump-header "$source_headers" \
        --output "$manifest" \
        --header "Accept: ${accept}" \
        "https://${registry}/v2/${repository}/manifests/${expected}"
    actual="$(awk 'BEGIN{IGNORECASE=1} /^Docker-Content-Digest:/ {gsub("\r", "", $2); print $2}' "$source_headers")"
    media_type="$(awk 'BEGIN{IGNORECASE=1} /^Content-Type:/ {$1=""; sub(/^ /, ""); gsub("\r", ""); print}' "$source_headers")"
    if [[ "$actual" != "$expected" || -z "$media_type" ]]; then
        printf 'source manifest validation failed for %s@%s\n' "$repository" "$expected" >&2
        exit 1
    fi

    tag_headers="$work/${service}-tag.headers"
    status="$(curl --silent --show-error \
        --user "$auth" \
        --dump-header "$tag_headers" \
        --output /dev/null \
        --write-out '%{http_code}' \
        --header "Accept: ${accept}" \
        "https://${registry}/v2/${repository}/manifests/${version}")"
    if [[ "$status" == 200 ]]; then
        tagged="$(awk 'BEGIN{IGNORECASE=1} /^Docker-Content-Digest:/ {gsub("\r", "", $2); print $2}' "$tag_headers")"
        if [[ "$tagged" != "$expected" ]]; then
            printf 'Harbor tag %s/%s:%s points to %s, expected %s\n' \
                "$registry" "$repository" "$version" "$tagged" "$expected" >&2
            exit 1
        fi
        continue
    fi
    if [[ "$status" != 404 ]]; then
        printf 'Harbor returned HTTP %s while checking %s:%s\n' "$status" "$repository" "$version" >&2
        exit 1
    fi

    curl --fail --silent --show-error \
        --user "$auth" \
        --request PUT \
        --header "Content-Type: ${media_type}" \
        --data-binary "@${manifest}" \
        "https://${registry}/v2/${repository}/manifests/${version}" \
        >/dev/null
done
