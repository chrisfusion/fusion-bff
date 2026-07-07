{{- define "fusion-bff.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "fusion-bff.fullname" -}}
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

{{- define "fusion-bff.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "fusion-bff.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "fusion-bff.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "fusion-bff.dbSecretName" -}}
{{- if .Values.db.create -}}
{{ include "fusion-bff.fullname" . }}-db
{{- else -}}
{{ .Values.db.existingSecret }}
{{- end -}}
{{- end }}

{{/*
Secret name holding the PostgreSQL admin/superuser credentials (key: "password")
used by the one-time create-database Job. Not the same secret as dbSecretName
above, which holds the app's own runtime DB_DSN.
*/}}
{{- define "fusion-bff.pgAdminSecretName" -}}
{{- if .Values.postgresql.external.existingSecret -}}
{{ .Values.postgresql.external.existingSecret }}
{{- else -}}
{{ include "fusion-bff.fullname" . }}-postgresql-admin
{{- end -}}
{{- end }}

{{- define "fusion-bff.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "fusion-bff.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "fusion-bff.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fusion-bff.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
