{{- define "periscope.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- define "periscope.fullname" -}}
{{- if .Values.fullnameOverride }}{{ .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}{{ else if eq .Release.Name (include "periscope.name" .) }}{{ .Release.Name | trunc 63 | trimSuffix "-" }}{{ else }}{{ printf "%s-%s" .Release.Name (include "periscope.name" .) | trunc 63 | trimSuffix "-" }}{{ end }}
{{- end }}
{{- define "periscope.labels" -}}
app.kubernetes.io/name: {{ include "periscope.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
{{- define "periscope.selectorLabels" -}}
app.kubernetes.io/name: {{ include "periscope.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "periscope.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}{{ default (include "periscope.fullname" .) .Values.serviceAccount.name }}{{ else }}{{ default "default" .Values.serviceAccount.name }}{{ end }}
{{- end }}
{{- define "periscope.secretName" -}}
{{- default (include "periscope.fullname" .) .Values.existingSecret.name }}
{{- end }}
{{- define "periscope.organizationEncryptionSecretName" -}}
{{- if .Values.organizationEncryption.existingSecret.name }}{{ .Values.organizationEncryption.existingSecret.name }}{{ else }}{{ default (printf "%s-org-encryption" (include "periscope.fullname" .)) .Values.organizationEncryption.secretName | trunc 63 | trimSuffix "-" }}{{ end }}
{{- end }}
{{- define "periscope.organizationEncryptionSecretKey" -}}
{{- if .Values.organizationEncryption.existingSecret.name }}{{ required "organizationEncryption.existingSecret.key is required" .Values.organizationEncryption.existingSecret.key }}{{ else }}{{ required "organizationEncryption.key is required" .Values.organizationEncryption.key }}{{ end }}
{{- end }}
