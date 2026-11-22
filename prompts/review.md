<<<UNTRUSTED-PAGE-BEFORE path={{.Path}}>>>
{{if .New}}[the page does not exist before the change; only its tool-owned provenance is shown]
{{end}}{{.Before}}
<<<END-UNTRUSTED-PAGE-BEFORE>>>

<<<UNTRUSTED-PAGE-AFTER path={{.Path}}>>>
{{.After}}
<<<END-UNTRUSTED-PAGE-AFTER>>>

<<<UNTRUSTED-SOURCE-DIFF>>>
{{- range .Hunks}}
Diff of {{.Path}}:

{{.Patch}}
{{end}}
<<<END-UNTRUSTED-SOURCE-DIFF>>>

Reply now with the JSON object for this page, and nothing else.
