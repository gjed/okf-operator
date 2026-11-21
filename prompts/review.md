<<<UNTRUSTED-PAGE-BEFORE path="{{.Path}}">>>
{{if .New}}[the page does not exist before the change]{{else}}{{.Before}}{{end}}
<<<END-UNTRUSTED-PAGE-BEFORE>>>

<<<UNTRUSTED-PAGE-AFTER path="{{.Path}}">>>
{{.After}}
<<<END-UNTRUSTED-PAGE-AFTER>>>

<<<UNTRUSTED-SOURCE-DIFF>>>
{{- range .Hunks}}
Diff of {{.Path}}:

{{.Patch}}
{{end}}
<<<END-UNTRUSTED-SOURCE-DIFF>>>

Reply now with the JSON object for the page at {{.Path}}, and nothing else.
