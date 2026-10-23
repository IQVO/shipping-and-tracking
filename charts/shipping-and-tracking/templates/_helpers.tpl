{{/*
Expand the name of the chart.
*/}}
{{- define "shipping-and-tracking.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "shipping-and-tracking.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "shipping-and-tracking.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "shipping-and-tracking.labels" -}}
helm.sh/chart: {{ include "shipping-and-tracking.chart" . }}
{{ include "shipping-and-tracking.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Every Deployment and Service also pins
app.kubernetes.io/component (api), so a Service never selects another workload.
*/}}
{{- define "shipping-and-tracking.selectorLabels" -}}
app.kubernetes.io/name: {{ include "shipping-and-tracking.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "shipping-and-tracking.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "shipping-and-tracking.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "shipping-and-tracking.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "shipping-and-tracking.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Name of the Secret holding SHIP_TO_ENCRYPTION_KEY and STOP_KEY_SECRET: the
operator's own (crypto.existingSecret) or the one this chart creates.
*/}}
{{- define "shipping-and-tracking.cryptoSecretName" -}}
{{- if .Values.crypto.existingSecret }}
{{- .Values.crypto.existingSecret }}
{{- else }}
{{- include "shipping-and-tracking.fullname" . }}-crypto
{{- end }}
{{- end }}

{{/*
Image reference.
*/}}
{{- define "shipping-and-tracking.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
MANDATORY: cmd/api exits at boot without DATABASE_URL; there is no in-memory
mode. Fails the render naming the values instead of crash-looping.
*/}}
{{- define "shipping-and-tracking.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "shipping-and-tracking requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without SHIP_TO_ENCRYPTION_KEY and
STOP_KEY_SECRET. They come from crypto.existingSecret or from both inline
values; neither ever reaches the ConfigMap.
*/}}
{{- define "shipping-and-tracking.requireCrypto" -}}
{{- if not .Values.crypto.existingSecret -}}
{{- if not .Values.crypto.shipToEncryptionKey -}}
{{- fail "shipping-and-tracking requires crypto.existingSecret or crypto.shipToEncryptionKey (SHIP_TO_ENCRYPTION_KEY, base64 of 32 bytes): the ship-to snapshot is always encrypted and the binary exits at boot without the key." -}}
{{- end -}}
{{- if not .Values.crypto.stopKeySecret -}}
{{- fail "shipping-and-tracking requires crypto.existingSecret or crypto.stopKeySecret (STOP_KEY_SECRET, at least 16 bytes): the binary exits at boot without it." -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS and rejects
EVENT_PUBLISHER=log. There is no default broker and no way to switch Kafka off.
*/}}
{{- define "shipping-and-tracking.requireKafka" -}}
{{- $_ := required "shipping-and-tracking requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- if ne (toString .Values.config.eventPublisher) "kafka" -}}
{{- fail (printf "config.eventPublisher is %q - the only publisher is \"kafka\": there is no log sink; the binary rejects EVENT_PUBLISHER=log at boot." (toString .Values.config.eventPublisher)) -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: customer-ordering's base URL (GetShipTo). The binary exits at boot
without CUSTOMER_ORDERING_URL.
*/}}
{{- define "shipping-and-tracking.requireCustomerOrdering" -}}
{{- if not .Values.config.customerOrderingUrl -}}
{{- fail "config.customerOrderingUrl is required (CUSTOMER_ORDERING_URL): labels need the ship-to of customer-ordering and cmd/api exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
All four consumers always run, each under a STABLE consumer group id from
values (cmd/api exits at boot without VENDOR_, LINEHAUL_, LASTMILE_ and
CATALOG_CONSUMER_GROUP). A stable id lets a rescheduled pod resume from its
committed offsets; it must never be derived from the pod name.
*/}}
{{- define "shipping-and-tracking.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "vendorConsumerGroup" "env" "VENDOR_CONSUMER_GROUP") (dict "key" "linehaulConsumerGroup" "env" "LINEHAUL_CONSUMER_GROUP") (dict "key" "lastmileConsumerGroup" "env" "LASTMILE_CONSUMER_GROUP") (dict "key" "catalogConsumerGroup" "env" "CATALOG_CONSUMER_GROUP") -}}
{{- if not (index $.Values.config $c.key) -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id, never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back a log publisher or an in-memory mode: no
EVENT_PUBLISHER other than kafka and no *_MODE variable at all.
*/}}
{{- define "shipping-and-tracking.requireNoInMemoryEnv" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- $value := toString (default "" .value) -}}
{{- if and (eq $name "EVENT_PUBLISHER") (ne $value "kafka") -}}
{{- fail (printf "extraEnv sets EVENT_PUBLISHER=%s - the only publisher is \"kafka\"." $value) -}}
{{- end -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s - *_MODE variables do not exist in shipping-and-tracking: it runs on real Postgres and real Kafka only, with no permissive or off mode." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The graceful shutdown is sequential (cmd/api/main.go): SHUTDOWN_DRAIN_DELAY +
HTTP drain bound (10s) + consumers and jobs drain bound (10s) + outbox relay
drain bound (10s). The grace period must EXCEED the sum, or the kubelet
SIGKILLs the pod between "consumers stopped" and "relay stopped", stranding an
uncommitted offset or an in-flight outbox batch. The drain delay must be whole
seconds ("5s", "5", "0") so the chart can add it up.
*/}}
{{- define "shipping-and-tracking.requireGracePeriod" -}}
{{- $raw := toString .Values.config.shutdownDrainDelay -}}
{{- if not (regexMatch "^[0-9]+s?$" $raw) -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want whole seconds (\"5s\", \"5\", \"0\") so the chart can check terminationGracePeriodSeconds against the shutdown bounds." $raw) -}}
{{- end -}}
{{- $drain := int (trimSuffix "s" $raw) -}}
{{- $floor := add 30 $drain -}}
{{- if le (int .Values.terminationGracePeriodSeconds) (int $floor) -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - want more than %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP 10s + consumers 10s + relay 10s): a shorter grace period SIGKILLs the consumers or the relay mid-drain." .Values.terminationGracePeriodSeconds $floor $drain) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment includes it, so a mis-set release fails `helm template`.
*/}}
{{- define "shipping-and-tracking.requireRealRuntime" -}}
{{- include "shipping-and-tracking.requireDatabase" . -}}
{{- include "shipping-and-tracking.requireCrypto" . -}}
{{- include "shipping-and-tracking.requireKafka" . -}}
{{- include "shipping-and-tracking.requireCustomerOrdering" . -}}
{{- include "shipping-and-tracking.requireConsumerGroups" . -}}
{{- include "shipping-and-tracking.requireNoInMemoryEnv" . -}}
{{- include "shipping-and-tracking.requireGracePeriod" . -}}
{{- end -}}
