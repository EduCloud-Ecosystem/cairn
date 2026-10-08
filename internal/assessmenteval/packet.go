// SPDX-License-Identifier: AGPL-3.0-or-later
package assessmenteval

import (
	"encoding/json"
	"html/template"
	"io"
)

// RenderPacket uses autoescaping for all evidence and feedback. No external
// resources, source execution, provider calls or browser persistence are used.
func RenderPacket(w io.Writer, r Report, digest string) error {
	worksheet, _ := json.Marshal(NewWorksheet(r, digest))
	return packet.Execute(w, struct {
		Report    Report
		Worksheet string
	}{r, string(worksheet)})
}

var packet = template.Must(template.New("packet").Funcs(template.FuncMap{
	"json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) },
	"score": func(v *float64) string {
		if v == nil {
			return "unassessable"
		}
		b, _ := json.Marshal(*v)
		return string(b)
	},
}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Cairn assessment review packet</title>
<style>body{font:17px/1.55 system-ui,sans-serif;max-width:1000px;margin:40px auto;padding:0 24px;color:#172c34;background:#f6f7f5}h1,h2,h3{line-height:1.25}article,fieldset{background:white;border:1px solid #bcc9c9;border-radius:8px;padding:20px;margin:20px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#eef2f1;padding:12px}label{display:block;margin:12px 0}input,select,textarea,button{font:inherit;padding:8px;max-width:100%;box-sizing:border-box}textarea{width:100%;min-height:80px}button{background:#164f53;color:white;border:0;border-radius:5px;cursor:pointer}table{border-collapse:collapse;width:100%;background:white}th,td{text-align:left;padding:9px;border-bottom:1px solid #ccd6d5}th{font-weight:500}details{margin:12px 0}summary{cursor:pointer}small{color:#40565b}.notice{border-left:4px solid #aa681d;padding:10px 18px;background:#fff4df}.controls{border:2px solid #164f53;padding:20px}a{color:#125d68}@media print{.controls,.human{display:none}body{background:white}}</style>
<h1>Assessment review packet</h1><p>Synthetic statistics · {{.Report.Trials}} trial(s) · {{.Report.CreatedAt}}</p>
<p class="notice">Agent-authored references are provisional. Check the source and rubric yourself. Valid score ranges and citation locations do not establish accurate or fair feedback. This packet never publishes grades or enables a course.</p>
<h2>Automated comparison</h2><table><tbody>
<tr><th scope="row">Valid proposals</th><td>{{.Report.Metrics.Proposals}}</td></tr>
<tr><th scope="row">Failed provider proposals</th><td>{{.Report.Metrics.ProviderFailures}}</td></tr>
<tr><th scope="row">Expected extraction blocks</th><td>{{.Report.Metrics.ExpectedBlocks}}</td></tr>
<tr><th scope="row">Unexpected outcomes</th><td>{{.Report.Metrics.UnexpectedOutcomes}}</td></tr>
<tr><th scope="row">Provisional criterion agreement</th><td>{{.Report.Metrics.ExactMatches}} / {{.Report.Metrics.CriterionComparisons}}</td></tr>
<tr><th scope="row">Zero / unassessable disagreements</th><td>{{.Report.Metrics.AssessabilityMismatches}}</td></tr>
<tr><th scope="row">Stable scores across completed pairs</th><td>{{.Report.Metrics.ScoreStableCases}} / {{.Report.Metrics.RepeatedCases}}</td></tr>
<tr><th scope="row">Reported input / output tokens</th><td>{{.Report.Metrics.InputTokens}} / {{.Report.Metrics.OutputTokens}}</td></tr>
</tbody></table><p>Score comparisons include null/unassessable agreement. Mean absolute point error includes only numeric pairs. Repeatability covers score/assessability across successful trials, not feedback wording. Citation counts establish location validity only; judge support below.</p>
<div class="controls"><h2>Human review</h2><label>Your reviewer name <input id="reviewer" autocomplete="name"></label><p>Read evidence first, enter your own points, then assess the proposed feedback. All four selections and a rationale are needed per criterion. Leave points empty when unassessable. Export saves a JSON worksheet for the offline review command; it does not submit anything.</p><label>Resume a saved worksheet <input type="file" id="import" accept="application/json,.json"></label><button id="export">Export review worksheet</button><p id="status" role="status"></p></div>
{{range .Report.Results}}<article><h2>{{.CaseID}} · trial {{.Trial}}</h2><p>Status: <strong>{{.Status}}</strong>{{if .Error}} · {{.Error}}{{end}}</p>
<h3>Rubric</h3>{{range .Document.Rubric.Criteria}}<p><strong>{{.ID}} ({{.MaxPoints}} points)</strong>: {{.Description}}</p>{{end}}
<h3>Captured source</h3>{{range .Document.Artifacts}}<h4>{{.Path}}</h4>{{if .Issue}}<p class="notice">{{.Issue}}</p>{{end}}{{range .Segments}}<pre>{{.Location}}: {{.Text}}</pre>{{end}}{{end}}
{{$case := .CaseID}}{{$trial := .Trial}}
{{if .Document.Proposal}}<details><summary>Show proposed feedback and scores</summary>{{range .Document.Proposal.Criteria}}<h3>{{.CriterionID}} · {{score .Points}}</h3><p>{{.Feedback}}</p><p>Uncertainty: {{.Uncertainty}}</p><pre>{{json .Citations}}</pre>{{end}}<p>Model: {{.Document.Proposal.Model}} · prompt: {{.Document.Proposal.PromptVersion}}</p></details>
{{range .Document.Proposal.Criteria}}<fieldset class="human" data-case="{{$case}}" data-trial="{{$trial}}" data-criterion="{{.CriterionID}}"><legend>Your judgment: {{.CriterionID}}</legend><label>Assessable <select data-field="assessable"><option value="">Choose</option><option value="true">Yes</option><option value="false">No</option></select></label><label>Your points <input data-field="points" type="number" min="0" step="any"></label><label>Proposed feedback supported by cited evidence <select data-field="evidence_supported"><option value="">Choose</option><option value="true">Yes</option><option value="false">No</option></select></label><label>Proposed feedback useful to the learner <select data-field="feedback_useful"><option value="">Choose</option><option value="true">Yes</option><option value="false">No</option></select></label><label>Uncertainty appropriate <select data-field="uncertainty_appropriate"><option value="">Choose</option><option value="true">Yes</option><option value="false">No</option></select></label><label>Rationale <textarea data-field="note" maxlength="4000"></textarea></label></fieldset>{{end}}{{end}}
</article>{{end}}
<details><summary>Provisional reference scores (inspect after your own judgment)</summary>{{range .Report.Cases}}<p>{{.ID}}: {{json .Expected}}</p>{{end}}</details>
<textarea id="review-json" hidden>{{.Worksheet}}</textarea><script>
document.getElementById('import').addEventListener('change',async event=>{
 try{
  const file=event.target.files[0];if(!file||file.size>1048576)throw Error();
  const saved=JSON.parse(await file.text()),base=JSON.parse(document.getElementById('review-json').value);
  if(saved.report_digest!==base.report_digest||!Array.isArray(saved.judgments)||typeof saved.reviewer!=='string')throw Error();
  const seen=new Set();
  for(const j of saved.judgments){
   const key=JSON.stringify([j.case_id,j.trial,j.criterion_id]);
   if(seen.has(key)||!base.judgments.some(b=>b.case_id===j.case_id&&b.trial===j.trial&&b.criterion_id===j.criterion_id))throw Error();seen.add(key);
   if(typeof j.note!=='string'||j.note.length>4000||(j.points!==null&&(typeof j.points!=='number'||!Number.isFinite(j.points))))throw Error();
   for(const k of ['assessable','evidence_supported','feedback_useful','uncertainty_appropriate'])if(j[k]!==null&&typeof j[k]!=='boolean')throw Error();
  }
  document.getElementById('reviewer').value=saved.reviewer;
  for(const row of document.querySelectorAll('.human')){
   const j=saved.judgments.find(j=>j.case_id===row.dataset.case&&j.trial===Number(row.dataset.trial)&&j.criterion_id===row.dataset.criterion);
   for(const el of row.querySelectorAll('[data-field]'))el.value=j&&j[el.dataset.field]!==null?String(j[el.dataset.field]):'';
  }
  document.getElementById('status').textContent='Worksheet restored locally. Validate it with the offline review command when finished.';
 }catch{document.getElementById('status').textContent='Could not import: choose a valid worksheet for this exact report.';}
});
document.getElementById('export').addEventListener('click',()=>{
 const w=JSON.parse(document.getElementById('review-json').value);
 w.reviewer=document.getElementById('reviewer').value.trim(); w.reviewed_at=new Date().toISOString();
 for(const row of document.querySelectorAll('.human')){
  const j=w.judgments.find(j=>j.case_id===row.dataset.case&&j.trial===Number(row.dataset.trial)&&j.criterion_id===row.dataset.criterion);
  for(const el of row.querySelectorAll('[data-field]')){
   const k=el.dataset.field, v=el.value;
   j[k]=k==='note'?v:(k==='points'?(v===''?null:Number(v)):(v===''?null:v==='true'));
  }
 }
 const url=URL.createObjectURL(new Blob([JSON.stringify(w,null,2)],{type:'application/json'}));
 const a=document.createElement('a');a.href=url;a.download='human-review.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
 document.getElementById('status').textContent='Worksheet exported. Incomplete entries remain pending; run the offline review command to validate.';
});</script></html>`))
