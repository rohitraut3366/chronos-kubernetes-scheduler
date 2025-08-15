{{/*
Expand the name of the chart.
*/}}
{{- define "chronos-kubernetes-scheduler.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "chronos-kubernetes-scheduler.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "chronos-kubernetes-scheduler.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "chronos-kubernetes-scheduler.labels" -}}
helm.sh/chart: {{ include "chronos-kubernetes-scheduler.chart" . }}
{{ include "chronos-kubernetes-scheduler.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "chronos-kubernetes-scheduler.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chronos-kubernetes-scheduler.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "chronos-kubernetes-scheduler.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "chronos-kubernetes-scheduler.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the image name
*/}}
{{- define "chronos-kubernetes-scheduler.image" -}}
{{- $registry := .Values.global.imageRegistry | default .Values.image.registry -}}
{{- $repository := .Values.image.repository -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- end }}

{{/*
Common annotations
*/}}
{{- define "chronos-kubernetes-scheduler.annotations" -}}
app.kubernetes.io/name: {{ include "chronos-kubernetes-scheduler.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ include "chronos-kubernetes-scheduler.chart" . }}
{{- end }}

{{/*
Pod Security Context
*/}}
{{- define "chronos-kubernetes-scheduler.podSecurityContext" -}}
{{- if .Values.podSecurityContext }}
{{- toYaml .Values.podSecurityContext }}
{{- else }}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
fsGroup: 65532
{{- end }}
{{- end }}

{{/*
Container Security Context  
*/}}
{{- define "chronos-kubernetes-scheduler.securityContext" -}}
{{- if .Values.securityContext }}
{{- toYaml .Values.securityContext }}
{{- else }}
allowPrivilegeEscalation: false
runAsNonRoot: true
runAsUser: 65532
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end }}
{{- end }}

{{/*
Resource limits and requests
*/}}
{{- define "chronos-kubernetes-scheduler.resources" -}}
{{- if .Values.resources }}
{{- toYaml .Values.resources }}
{{- else }}
limits:
  cpu: 500m
  memory: 512Mi
requests:
  cpu: 100m
  memory: 128Mi
{{- end }}
{{- end }}

{{/*
Node selector
*/}}
{{- define "chronos-kubernetes-scheduler.nodeSelector" -}}
{{- if .Values.nodeSelector }}
{{- toYaml .Values.nodeSelector }}
{{- else }}
kubernetes.io/os: linux
{{- end }}
{{- end }}

{{/*
Tolerations
*/}}
{{- define "chronos-kubernetes-scheduler.tolerations" -}}
{{- if .Values.tolerations }}
{{- toYaml .Values.tolerations }}
{{- else }}
- key: node-role.kubernetes.io/control-plane
  operator: Exists
  effect: NoSchedule
- key: node-role.kubernetes.io/master
  operator: Exists
  effect: NoSchedule
{{- end }}
{{- end }}

{{/*
Create a default affinity rule
*/}}
{{- define "chronos-kubernetes-scheduler.affinity" -}}
{{- if .Values.affinity }}
{{- toYaml .Values.affinity }}
{{- else }}
nodeAffinity:
  preferredDuringSchedulingIgnoredDuringExecution:
  - weight: 100
    preference:
      matchExpressions:
      - key: node-role.kubernetes.io/control-plane
        operator: Exists
  - weight: 50
    preference:
      matchExpressions:
      - key: node-role.kubernetes.io/master
        operator: Exists
{{- end }}
{{- end }}
