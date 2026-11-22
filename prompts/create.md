{{- if .Profile}}<<<WRITING-GUIDANCE>>>
{{.Profile}}
<<<END-WRITING-GUIDANCE>>>

{{end -}}
<<<PAGE-TEMPLATE type="{{.Type}}">>>
{{.Template}}
<<<END-PAGE-TEMPLATE>>>

{{range .References}}<<<REFERENCE-PAGE path="{{.Path}}">>>
{{.Content}}
<<<END-REFERENCE-PAGE>>>

{{end -}}
<<<UNTRUSTED-SOURCE-DATA>>>
{{- if .PROmitted}}
[{{.PROmitted}} older pull requests left out]
{{end}}
{{- range .PullRequests}}
Pull request #{{.Number}}: {{.Title}}

{{.Body}}{{if .BodyTruncated}}
[body truncated]{{end}}
{{end}}
{{- range .Hunks}}
Diff of {{.Path}}:

{{.Patch}}
{{end}}
{{- range .Files}}
Full content of {{.Path}}:

{{.Content}}
{{end}}
<<<END-UNTRUSTED-SOURCE-DATA>>>
{{- if .ValidationError}}

Your previous reply for this page was rejected: {{.ValidationError}}
{{- end}}

Reply now with the complete new page at {{.Path}}, with `type: {{.Type}}` in its frontmatter, and nothing else.
