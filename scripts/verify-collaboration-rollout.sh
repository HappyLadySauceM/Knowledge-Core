#!/usr/bin/env bash
set -euo pipefail

namespace="${KC_SMOKE_NAMESPACE:-knowledge-core-dev}"
statefulset="${KC_SMOKE_COLLABORATION_STATEFULSET:-knowledge-core-collaboration}"
selector="app.kubernetes.io/name=${statefulset}"

statefulset_json="$(kubectl --namespace "$namespace" get statefulset "$statefulset" -o json)"
desired="$(jq -r '.spec.replicas // 0' <<<"$statefulset_json")"
ready="$(jq -r '.status.readyReplicas // 0' <<<"$statefulset_json")"
updated="$(jq -r '.status.updatedReplicas // 0' <<<"$statefulset_json")"
current_revision="$(jq -r '.status.currentRevision // ""' <<<"$statefulset_json")"
update_revision="$(jq -r '.status.updateRevision // ""' <<<"$statefulset_json")"

if [[ "$desired" == "0" || "$ready" != "$desired" || "$updated" != "$desired" ]]; then
	echo "Collaboration rollout is not complete: desired=${desired} ready=${ready} updated=${updated}" >&2
	exit 1
fi
if [[ -z "$current_revision" || "$current_revision" != "$update_revision" ]]; then
	echo "Collaboration StatefulSet revisions are still mixed: current=${current_revision:-<none>} update=${update_revision:-<none>}" >&2
	exit 1
fi

pods_json="$(kubectl --namespace "$namespace" get pods --selector "$selector" -o json)"
active_count="$(jq '[.items[] | select(.metadata.deletionTimestamp == null)] | length' <<<"$pods_json")"
ready_count="$(jq '[.items[] | select(.metadata.deletionTimestamp == null and any(.status.containerStatuses[]?; .name == "collaboration" and .ready == true))] | length' <<<"$pods_json")"
if [[ "$active_count" != "$desired" || "$ready_count" != "$desired" ]]; then
	echo "Collaboration pods are not uniformly ready: desired=${desired} active=${active_count} ready=${ready_count}" >&2
	exit 1
fi

mapfile -t image_ids < <(jq -r '.items[] | select(.metadata.deletionTimestamp == null) | .status.containerStatuses[]? | select(.name == "collaboration") | .imageID' <<<"$pods_json" | sort -u)
if [[ "${#image_ids[@]}" != "1" || -z "${image_ids[0]:-}" ]]; then
	echo "Collaboration pods do not share one immutable image: ${image_ids[*]:-<none>}" >&2
	exit 1
fi

echo "Collaboration rollout gate passed: ${desired} ready pod(s), revision=${current_revision}, image=${image_ids[0]}"
