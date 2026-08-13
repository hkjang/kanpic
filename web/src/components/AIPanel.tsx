import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Bot, Check, ChevronDown, Clipboard, Clock3, Eye, HelpCircle, ListChecks, RefreshCw, RotateCcw, Send, ShieldCheck, Sparkles, Terminal, Wrench, XCircle } from 'lucide-react'
import { FormEvent, useEffect, useState } from 'react'
import { api, newIdempotencyKey } from '../lib/api'
import { collaborationClientId } from '../lib/client'
import type { AIAction, AIConfig, AIExecutionResult, AICellSnapshot, AIPromptPreview, AIUsage, AgentContext, AgentExecutionResult, AgentRun } from '../types'

type Mode=AIAction['mode']
type Props={workbookId:string;workbookName?:string;sheetId:string;sheetName?:string;selectionRange:string;baseVersion:number;initialMode?:Mode;initialRequest?:string;onClose:()=>void;onExecuted:(result:AIExecutionResult)=>void}

const modeLabel:Record<Mode,string>={formula:'수식 생성',explain:'수식 설명',fix:'수식 오류 수정',summarize:'범위 분석',anomaly:'이상치 탐지',clean:'데이터 정제',format:'자동 서식',chart:'차트 생성',agent:'복합 작업'}

/** What each mode does, whether it writes, and a request that works well. */
const MODES:Array<{id:Mode;hint:string;writes:boolean;examples:string[]}>=[
  {id:'summarize',hint:'선택 범위의 핵심 지표와 패턴을 정리합니다.',writes:false,examples:['핵심 지표와 눈에 띄는 변화를 3줄로 요약해줘','제품별 매출 비중과 상위 항목을 알려줘']},
  {id:'anomaly',hint:'평균이나 추세에서 벗어난 값을 찾아 알려 줍니다.',writes:false,examples:['평균에서 크게 벗어난 값을 찾아줘','전월 대비 급격히 변한 항목을 알려줘']},
  {id:'explain',hint:'선택한 수식의 계산 방식과 참조를 설명합니다.',writes:false,examples:['이 수식이 무엇을 계산하는지 설명해줘','참조하는 범위와 계산 순서를 알려줘']},
  {id:'formula',hint:'요청한 계산을 수행하는 수식을 제안합니다. 승인해야 적용됩니다.',writes:true,examples:['각 행의 합계를 오른쪽 빈 열에 계산하는 수식','전월 대비 증감율을 계산하는 수식을 만들어줘']},
  {id:'fix',hint:'오류가 난 수식의 원인을 찾아 수정안을 제안합니다.',writes:true,examples:['#REF! 오류의 원인을 찾아 고쳐줘','합계가 비어 있는 수식을 수정해줘']},
  {id:'clean',hint:'공백·대소문자·날짜 형식 등을 일관되게 정리합니다.',writes:true,examples:['앞뒤 공백을 없애고 날짜 형식을 YYYY-MM-DD로 통일해줘','전화번호 형식을 010-0000-0000으로 맞춰줘']},
  {id:'format',hint:'헤더·데이터 유형을 분석해 안전한 셀 서식을 제안합니다.',writes:true,examples:['이 데이터를 보기 좋게 정리해줘','헤더와 숫자 형식을 보고서 스타일로 바꿔줘']},
  {id:'chart',hint:'선택 범위와 계열을 검증한 뒤 적합한 차트를 제안합니다.',writes:true,examples:['월별 매출 차트를 만들어줘','카테고리별 값을 막대 차트로 보여줘']},
  {id:'agent',hint:'수식·서식·차트를 조합하는 복합 계획을 만듭니다.',writes:true,examples:['이 파일을 경영진 보고용으로 정리해줘','분석하고 핵심 차트까지 만들어줘']},
]

const snapshotText=(snapshot:AICellSnapshot)=>snapshot.formula||(snapshot.value===undefined||snapshot.value===null||snapshot.value===''?'(빈 셀)':typeof snapshot.value==='string'?snapshot.value:JSON.stringify(snapshot.value))

/** Cells a range covers, used to warn before the server refuses the request. */
export function rangeCellCount(range:string){
  const parts=range.split(':')
  const parse=(value:string)=>{
    const match=/^([A-Z]+)(\d+)$/.exec(value.trim().toUpperCase())
    if(!match)return undefined
    let column=0
    for(const letter of match[1])column=column*26+letter.charCodeAt(0)-64
    return {row:Number(match[2]),column}
  }
  const start=parse(parts[0]),end=parse(parts[1]??parts[0])
  if(!start||!end)return 0
  return (Math.abs(end.row-start.row)+1)*(Math.abs(end.column-start.column)+1)
}

export function AIPanel({workbookId,workbookName,sheetId,sheetName,selectionRange,baseVersion,initialMode='summarize',initialRequest='',onClose,onExecuted}:Props){
  const client=useQueryClient()
  const [mode,setMode]=useState<Mode>(initialMode)
  const [request,setRequest]=useState(initialRequest)
  const [run,setRun]=useState<AgentRun>()
  const [conversationId,setConversationId]=useState<string>()
  const [guideOpen,setGuideOpen]=useState(false)
  const [promptOpen,setPromptOpen]=useState(false)
  const [elapsed,setElapsed]=useState(0)
  const config=useQuery({queryKey:['ai-config'],queryFn:()=>api<AIConfig>('/api/v1/ai/config')})
  const context=useQuery({queryKey:['agent-context',workbookId,sheetId,selectionRange],queryFn:()=>api<AgentContext>(`/api/v1/workbooks/${workbookId}/agent/context?sheet_id=${encodeURIComponent(sheetId)}&selection=${encodeURIComponent(selectionRange)}`),enabled:Boolean(config.data?.enabled)})
  const recent=useQuery({queryKey:['agent-runs',workbookId],queryFn:()=>api<{items:AgentRun[]}>(`/api/v1/workbooks/${workbookId}/agent/runs?limit=8`)})
  const refresh=async()=>Promise.all([client.invalidateQueries({queryKey:['agent-runs',workbookId]}),client.invalidateQueries({queryKey:['ai-actions',workbookId]})])
  const selectedCells=rangeCellCount(selectionRange)
  const overLimit=Boolean(config.data&&selectedCells>config.data.max_input_cells)
  const active=MODES.find(item=>item.id===mode)??MODES[0]
  const plan=useMutation({
    mutationFn:()=>{const key=newIdempotencyKey();return api<AgentRun>(`/api/v1/workbooks/${workbookId}/agent/messages`,{method:'POST',headers:{'Idempotency-Key':key},body:JSON.stringify({sheet_id:sheetId,selection:selectionRange,message:request.trim(),mode,conversation_id:conversationId,base_version:baseVersion,idempotency_key:key,client_id:collaborationClientId()})})},
    onSuccess:async item=>{setRun(item);setConversationId(item.conversation_id);await refresh()},
  })
  // The prompt preview is fetched on demand so opening the panel never reads
  // cells that nobody asked to send.
  const preview=useQuery({
    queryKey:['ai-prompt-preview',workbookId,sheetId,selectionRange,mode,request.trim()],
    queryFn:()=>api<AIPromptPreview>('/api/v1/ai/prompt:preview',{method:'POST',body:JSON.stringify({workbook_id:workbookId,sheet_id:sheetId,range:selectionRange,request:request.trim()||'(요청 없음)',mode,base_version:baseVersion})}),
    enabled:promptOpen&&!overLimit,
    staleTime:15_000,
  })
  const approve=useMutation({
    mutationFn:(item:AgentRun)=>api<AgentExecutionResult>(`/api/v1/agent/runs/${item.id}/approve`,{method:'POST',body:JSON.stringify({idempotency_key:newIdempotencyKey(),client_id:collaborationClientId(),expected_revision:item.action.revision})}),
    onSuccess:async result=>{setRun(result.run);if(result.operation)onExecuted({action:result.run.action,operation:result.operation,changes:result.changes});await Promise.all([refresh(),client.invalidateQueries({queryKey:['workbook',workbookId]}),client.invalidateQueries({queryKey:['charts',workbookId]})])},
  })
  const undo=useMutation({
    mutationFn:(item:AgentRun)=>api<AgentExecutionResult>(`/api/v1/changesets/${item.change_set_id}/rollback`,{method:'POST',body:JSON.stringify({idempotency_key:newIdempotencyKey(),client_id:collaborationClientId(),expected_revision:item.action.revision})}),
    onSuccess:async result=>{setRun(result.run);if(result.operation)onExecuted({action:result.run.action,operation:result.operation,changes:result.changes});await Promise.all([refresh(),client.invalidateQueries({queryKey:['workbook',workbookId]}),client.invalidateQueries({queryKey:['charts',workbookId]})])},
  })
  const cancel=useMutation({
    mutationFn:(item:AgentRun)=>api<AgentRun>(`/api/v1/agent/runs/${item.id}/cancel`,{method:'POST',body:JSON.stringify({idempotency_key:newIdempotencyKey(),client_id:collaborationClientId(),expected_revision:item.action.revision})}),
    onSuccess:async item=>{setRun(item);await refresh()},
  })
  // A gateway round trip takes seconds, so the wait is counted out loud.
  useEffect(()=>{
    if(!plan.isPending){setElapsed(0);return}
    const started=Date.now()
    const timer=window.setInterval(()=>setElapsed(Math.round((Date.now()-started)/1000)),500)
    return()=>window.clearInterval(timer)
  },[plan.isPending])
  const submit=(event:FormEvent)=>{event.preventDefault();if(request.trim()&&config.data?.enabled&&!overLimit)plan.mutate()}
  const loadRun=async(id:string)=>{const item=await api<AgentRun>(`/api/v1/agent/runs/${id}`);setRun(item);setConversationId(item.conversation_id)}
  const error=plan.error||approve.error||undo.error||cancel.error
  return <aside className="ai-panel managed-ai-panel" aria-label="AI 도우미 패널">
    <div className="ai-panel-head"><span><Bot/> Workbook Agent</span><div><button className="ai-head-help" aria-label="사용 가이드 열기" aria-expanded={guideOpen} title="사용 가이드" onClick={()=>setGuideOpen(current=>!current)}><HelpCircle/></button><button onClick={onClose} aria-label="Workbook Agent 닫기">×</button></div></div>
    <div className="ai-scroll">
      {guideOpen&&<UsageGuide config={config.data} onClose={()=>setGuideOpen(false)}/>}
      {config.isLoading&&<div className="ai-state"><RefreshCw className="spin"/>AI 설정을 확인하는 중…</div>}
      {config.isError&&<div className="ai-state error"><AlertTriangle/>AI 설정을 불러오지 못했습니다.</div>}
      {config.data&&!config.data.enabled&&<div className="ai-disabled"><Bot/><strong>AI가 아직 비활성화되어 있습니다.</strong><p>관리자가 사내 LLM Gateway를 검증하고 활성화하면 선택 범위 기반 계획을 사용할 수 있습니다.</p><a href="/admin?tab=settings">관리자 AI 설정 열기</a><button className="ai-inline-link" onClick={()=>setGuideOpen(true)}>사용 가이드 먼저 보기</button></div>}
      {config.data?.enabled&&<>
        <section className="agent-context" aria-label="현재 워크북 문맥"><header><Sparkles/><strong>현재 문맥</strong></header><dl><dt>Workbook</dt><dd>{run?.context.workbook_title||context.data?.workbook_title||workbookName||workbookId}</dd><dt>Sheet</dt><dd>{run?.context.active_sheet.name||context.data?.active_sheet?.name||sheetName||sheetId}</dd><dt>Selection</dt><dd>{selectionRange}</dd></dl></section>
        <div className={`ai-scope${overLimit?' over':''}`}><ShieldCheck/><div><strong>{selectionRange} · {selectedCells.toLocaleString()}셀만 모델에 전달</strong><small>{overLimit?`한 번에 최대 ${config.data.max_input_cells.toLocaleString()}셀까지 보낼 수 있습니다. 범위를 좁혀 주세요.`:`최대 ${config.data.max_input_cells.toLocaleString()}셀 · 모델 ${config.data.model}`}</small></div></div>
        {run&&<AgentTimeline run={run}/>}
        {!run&&<>
          <div className="ai-intro"><div className="ai-glyph"><Sparkles/></div><h3>워크북과 함께 일하는 Agent</h3><p>선택과 시트 구조를 읽고 계획합니다. 변경은 Diff를 검토하고 승인한 뒤에만 적용됩니다.</p></div>
          <form className="ai-composer" onSubmit={submit}>
            <div className="ai-mode-grid" role="radiogroup" aria-label="AI 작업 유형">
              {MODES.map(item=><button type="button" key={item.id} role="radio" aria-checked={mode===item.id} className={mode===item.id?'active':''} onClick={()=>setMode(item.id)}>
                <strong>{modeLabel[item.id]}</strong><em>{item.writes?'변경 제안':'읽기 전용'}</em>
              </button>)}
            </div>
            <p className="ai-mode-hint">{active.hint}</p>
            <div className="ai-examples">{(context.data?.suggested_prompts?.length?context.data.suggested_prompts:active.examples).map(example=><button type="button" key={example} onClick={()=>setRequest(example)}>{example}</button>)}</div>
            <textarea aria-label="AI 요청" value={request} maxLength={4000}
              onChange={event=>setRequest(event.target.value)}
              onKeyDown={event=>{if((event.ctrlKey||event.metaKey)&&event.key==='Enter'){event.preventDefault();submit(event)}}}
              placeholder={`예: ${active.examples[0]}`}/>
            <div className="ai-composer-foot"><small>{request.length.toLocaleString()} / 4,000자 · Ctrl/⌘+Enter로 실행</small></div>
            <button className="primary" disabled={!request.trim()||plan.isPending||overLimit}>{plan.isPending?<RefreshCw className="spin"/>:<Send/>} {plan.isPending?`계획 생성 중 ${elapsed}초`:'분석 및 계획 미리보기'}</button>
            <small><Eye/> 승인 전에는 워크북을 변경하지 않습니다.</small>
          </form>
          <PromptDisclosure open={promptOpen} onToggle={()=>setPromptOpen(current=>!current)} preview={preview.data} loading={preview.isFetching} error={preview.isError} disabled={overLimit}/>
        </>}
        {run&&<><AgentPlanView run={run}/><ActionPreview action={run.action} onApprove={()=>approve.mutate(run)} onCancel={()=>cancel.mutate(run)} onUndo={()=>undo.mutate(run)} pending={approve.isPending||undo.isPending||cancel.isPending} onNew={()=>{setRun(undefined);setPromptOpen(false)}} onRetry={()=>{setMode(run.action.mode);setRequest(run.action.request);setRun(undefined)}}/></>}
        {error&&<div className="ai-error" role="alert"><AlertTriangle/>{error instanceof Error?error.message:'AI 작업을 처리하지 못했습니다.'}</div>}
        {(recent.data?.items.length??0)>0&&<div className="ai-history"><div><Clock3/><strong>Agent 작업 이력</strong></div>{recent.data?.items.slice(0,5).map(item=><button key={item.id} onClick={()=>void loadRun(item.id)}><span>{modeLabel[item.action.mode]} · {item.selection}</span><small>{agentStateLabel(item.state)} · {new Date(item.started_at).toLocaleString('ko-KR')}</small></button>)}</div>}
      </>}
    </div>
  </aside>
}

/** Shows the exact request that would leave the building, on demand. */
function PromptDisclosure({open,onToggle,preview,loading,error,disabled}:{open:boolean;onToggle:()=>void;preview?:AIPromptPreview;loading:boolean;error:boolean;disabled:boolean}){
  const copy=async(text:string)=>{try{await navigator.clipboard?.writeText(text)}catch{window.prompt('복사할 내용',text)}}
  return <section className="ai-prompt-view">
    <button className="ai-disclosure" aria-expanded={open} onClick={onToggle}><Terminal/><span>모델에 보내는 내용 보기</span><ChevronDown className={open?'open':''}/></button>
    {open&&<div className="ai-disclosure-body">
      {disabled&&<p className="ai-note">선택 범위가 상한을 넘어 미리 볼 수 없습니다.</p>}
      {!disabled&&loading&&<p className="ai-note"><RefreshCw className="spin"/> 보낼 내용을 계산하는 중…</p>}
      {!disabled&&error&&<p className="ai-note error">보낼 내용을 불러오지 못했습니다.</p>}
      {!disabled&&preview&&<>
        <div className="ai-prompt-meta"><span>모델 {preview.model}</span><span>셀 {preview.cell_count.toLocaleString()}개</span><span>temperature {preview.temperature}</span><span>max_tokens {preview.max_tokens.toLocaleString()}</span>{preview.context_window?<span>컨텍스트 {preview.context_window.toLocaleString()}</span>:null}{preview.estimated_prompt_tokens?<span>입력 추정 {preview.estimated_prompt_tokens.toLocaleString()}</span>:null}</div>
        <p className="ai-note">{preview.context_window?`모델의 컨텍스트 ${preview.context_window.toLocaleString()}토큰에서 입력 추정치를 빼고 응답 상한을 자동으로 잡았습니다.`:'게이트웨이가 컨텍스트 길이를 알려 주지 않아 보수적인 상한을 사용합니다. 응답이 잘리면 자동으로 더 크게 다시 요청합니다.'}</p>
        <div className="ai-prompt-block"><header>시스템 프롬프트<button aria-label="시스템 프롬프트 복사" onClick={()=>void copy(preview.system_prompt)}><Clipboard/></button></header><pre>{preview.system_prompt}</pre></div>
        <div className="ai-prompt-block"><header>전달되는 데이터<button aria-label="전달 데이터 복사" onClick={()=>void copy(preview.user_content)}><Clipboard/></button></header><pre>{preview.user_content}</pre></div>
        <p className="ai-note">값이 비어 있는 셀과 선택 범위 밖의 데이터는 전송하지 않습니다.</p>
      </>}
    </div>}
  </section>
}

/** The panel is useless without knowing what it will and will not do. */
function UsageGuide({config,onClose}:{config?:AIConfig;onClose:()=>void}){
  return <section className="ai-guide" aria-label="AI 사용 가이드">
    <header><strong><HelpCircle/> 사용 가이드</strong><button onClick={onClose} aria-label="사용 가이드 닫기">×</button></header>
    <ol>
      <li><strong>범위를 선택합니다.</strong> 시트에서 분석하거나 고칠 범위를 먼저 고릅니다. 선택한 범위 안의 값이 있는 셀만 전송됩니다.</li>
      <li><strong>작업 유형을 고릅니다.</strong> 요약·이상치·설명은 읽기 전용이고, 수식·정제·서식·차트·복합 작업은 변경을 제안합니다.</li>
      <li><strong>계획을 검토합니다.</strong> 셀 Diff와 실행할 도구, 위험도, 검증 단계를 적용 전에 보여 줍니다.</li>
      <li><strong>승인해야 적용됩니다.</strong> 승인한 ChangeSet만 반영되고 <em>Undo</em>로 수식·차트·Agent 생성 시트를 함께 되돌릴 수 있습니다.</li>
    </ol>
    <dl>
      <dt>전송되는 것</dt><dd>요청, 워크북·시트 메타데이터, 선택 범위의 주소·값·수식과 의미 프로필{config?`, 최대 ${config.max_input_cells.toLocaleString()}셀`:''}</dd>
      <dt>전송되지 않는 것</dt><dd>다른 시트와 범위 밖의 셀 값, 공유 대상, 댓글, 계정 정보</dd>
      <dt>모델이 할 수 없는 것</dt><dd>승인 없는 변경, 외부 링크·스크립트 실행, 범위 밖 셀 수정{config?`, 한 번에 ${config.max_changes.toLocaleString()}셀 초과 변경`:''}</dd>
    </dl>
    <p className="ai-note">요청은 구체적일수록 좋습니다. “정리해줘”보다 “앞뒤 공백을 없애고 날짜를 YYYY-MM-DD로 통일해줘”처럼 규칙을 적어 주세요.</p>
  </section>
}

function AgentTimeline({run}:{run:AgentRun}){
  return <section className="agent-timeline" aria-label="Agent 대화">
    {run.messages.map(message=><article className={message.role} key={message.id}><strong>{message.role==='user'?'사용자':'Agent'}</strong><p>{message.content}</p></article>)}
  </section>
}

function AgentPlanView({run}:{run:AgentRun}){
  return <section className="agent-plan" aria-label="Agent 실행 계획">
    <header><ListChecks/><div><strong>{run.plan.goal}</strong><small>{riskLabel(run.risk)} · {agentStateLabel(run.state)}</small></div></header>
    <ol>{run.plan.steps.map(step=><li className={step.status} key={step.id||step.position}><span>{step.status==='completed'?<Check/>:step.status==='failed'?<AlertTriangle/>:step.status==='cancelled'?<XCircle/>:<Clock3/>}</span><div><strong>{step.description}</strong><small><code>{step.tool}</code> · {stepStatusLabel(step.status)}</small></div></li>)}</ol>
    {run.action.tool_calls?.length>0&&<div className="agent-tools"><Wrench/><span>{run.action.tool_calls.map(tool=>`${tool.name} (${stepStatusLabel(tool.status)})`).join(', ')}</span></div>}
  </section>
}

function ActionPreview({action,onApprove,onCancel,onUndo,pending,onNew,onRetry}:{action:AIAction;onApprove:()=>void;onCancel:()=>void;onUndo:()=>void;pending:boolean;onNew:()=>void;onRetry:()=>void}){
  const readOnly=action.mode==='explain'||action.mode==='summarize'||action.mode==='anomaly'
  const findings=action.findings??[]
  // Usage arrives with a fresh plan and is kept in the audit event afterwards.
  const usage=action.usage??(action.events??[]).map(event=>event.payload?.usage as AIUsage|undefined).find(Boolean)
  return <div className="ai-action-preview">
    <div className="ai-action-title"><div><span>{modeLabel[action.mode]}</span><strong>{action.summary}</strong></div><em className={`ai-status ${action.status}`}>{statusLabel(action.status,action.mode)}</em></div>
    <p className="ai-action-request">요청: {action.request}</p>
    <p>{action.explanation}</p>
    {findings.length>0&&<div className="ai-finding-list">{findings.map((finding,index)=><article className={finding.severity} key={`${finding.address||'range'}:${index}`}><header><span>{finding.address||'전체 범위'}</span><em>{severityLabel(finding.severity)}</em></header><strong>{finding.title}</strong><p>{finding.description}</p>{finding.address&&<code>현재: {snapshotText(finding.cell??{})}</code>}</article>)}</div>}
    {action.changes.length>0&&<div className="ai-change-list">{action.changes.map(change=><article key={`${change.row}:${change.column}`}><strong>{change.address}</strong><div><small>현재</small><code>{action.mode==='format'?JSON.stringify(change.before.style??{}):snapshotText(change.before)}</code></div><span>→</span><div><small>제안</small><code>{action.mode==='format'?JSON.stringify(change.after.style??{}):snapshotText(change.after)}</code></div></article>)}</div>}
    {action.tool_calls?.map(tool=><article className="agent-tool-preview" key={tool.id||tool.idempotency_key}><header><Wrench/><strong>{tool.name}</strong><em>{riskLabel(tool.risk)}</em></header><pre>{JSON.stringify(tool.arguments,null,2)}</pre></article>)}
    <div className="ai-plan-meta"><span>기준 버전 v{action.base_version}</span>{action.changes.length>0&&<span>{action.changes.length}셀 변경</span>}{findings.length>0&&<span>{findings.length}개 발견</span>}<span>{action.model}</span>{usage?.prompt_tokens?<span>입력 {usage.prompt_tokens.toLocaleString()}토큰</span>:null}{usage?.completion_tokens?<span>응답 {usage.completion_tokens.toLocaleString()}토큰</span>:null}{usage&&(usage.attempts??1)>1?<span>재시도 {(usage.attempts??1)-1}회</span>:null}</div>
    {action.status==='failed'&&<div className="ai-error"><AlertTriangle/>{action.error_message||'작업이 실패했습니다.'}</div>}
    {action.status==='planned'&&!readOnly&&<div className="ai-approval"><p><ShieldCheck/> 위 변경만 하나의 ChangeSet으로 적용되고 전체 Undo할 수 있습니다.</p><div><button onClick={onCancel} disabled={pending}><XCircle/> 취소</button><button className="primary" aria-label="검토한 계획 승인 및 변경 적용" onClick={onApprove} disabled={pending}><Check/> 변경 적용</button></div></div>}
    {(action.status==='completed'||(action.status==='planned'&&readOnly))&&<div className="ai-explanation-done"><Check/> 읽기 전용 분석이 완료됐으며 워크북 변경은 없습니다.</div>}
    {(action.status==='applying'||action.status==='undoing')&&<div className="ai-state"><RefreshCw className="spin"/>서버 작업을 마무리하는 중…</div>}
    {action.status==='applied'&&<div className="ai-applied"><Check/><div><strong>승인한 변경이 적용되었습니다.</strong><small>서버 버전 v{action.operation?.server_version}</small></div><button onClick={onUndo} disabled={pending}><RotateCcw/> Undo</button></div>}
    {action.status==='undone'&&<div className="ai-explanation-done"><RotateCcw/> AI 변경을 새 서버 버전으로 되돌렸습니다.</div>}
    <div className="ai-action-actions"><button onClick={onRetry}>같은 요청 고쳐 쓰기</button><button onClick={onNew}>새 요청 작성</button></div>
  </div>
}

function statusLabel(status:AIAction['status'],mode:AIAction['mode']){if(status==='completed')return mode==='summarize'?'요약 완료':mode==='anomaly'?'탐지 완료':'설명 완료';return {planned:'승인 대기',applying:'적용 중',applied:'적용됨',undoing:'취소 중',undone:'취소됨',failed:'실패',cancelled:'취소됨'}[status]}
function severityLabel(severity:'info'|'warning'|'critical'){return {info:'정보',warning:'주의',critical:'심각'}[severity]}
function agentStateLabel(state:AgentRun['state']){return {THINKING:'생각 중',READING_WORKBOOK:'워크북 읽는 중',PLANNING:'계획 중',WAITING_APPROVAL:'승인 대기',EXECUTING:'실행 중',VALIDATING:'검증 중',COMPLETED:'완료',FAILED:'실패',CANCELLED:'취소됨'}[state]}
function stepStatusLabel(status:string){return {completed:'완료',waiting_approval:'승인 대기',pending:'대기',executing:'진행',validating:'검증',failed:'실패',cancelled:'취소',planned:'계획됨'}[status]||status}
function riskLabel(risk:AIAction['risk']){return {READ:'읽기',LOW:'낮은 위험',MEDIUM:'중간 위험',HIGH:'높은 위험',CRITICAL:'매우 높은 위험'}[risk]||risk}
