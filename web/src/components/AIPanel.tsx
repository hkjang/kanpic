import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Bot, Check, Clock3, Eye, RefreshCw, RotateCcw, Send, ShieldCheck, Sparkles } from 'lucide-react'
import { FormEvent, useState } from 'react'
import { api, newIdempotencyKey } from '../lib/api'
import { collaborationClientId } from '../lib/client'
import type { AIAction, AIConfig, AIExecutionResult, AICellSnapshot } from '../types'

type Props={workbookId:string;sheetId:string;selectionRange:string;baseVersion:number;onClose:()=>void;onExecuted:(result:AIExecutionResult)=>void}

const modeLabel:Record<AIAction['mode'],string>={formula:'수식 생성',explain:'수식 설명',fix:'수식 오류 수정'}
const snapshotText=(snapshot:AICellSnapshot)=>snapshot.formula||(snapshot.value===undefined||snapshot.value===null||snapshot.value===''?'(빈 셀)':typeof snapshot.value==='string'?snapshot.value:JSON.stringify(snapshot.value))

export function AIPanel({workbookId,sheetId,selectionRange,baseVersion,onClose,onExecuted}:Props){
  const client=useQueryClient()
  const [mode,setMode]=useState<AIAction['mode']>('formula')
  const [request,setRequest]=useState('')
  const [action,setAction]=useState<AIAction>()
  const config=useQuery({queryKey:['ai-config'],queryFn:()=>api<AIConfig>('/api/v1/ai/config')})
  const recent=useQuery({queryKey:['ai-actions',workbookId],queryFn:()=>api<{items:AIAction[]}>(`/api/v1/workbooks/${workbookId}/ai/actions?limit=8`)})
  const refresh=async()=>client.invalidateQueries({queryKey:['ai-actions',workbookId]})
  const plan=useMutation({
    mutationFn:()=>{const key=newIdempotencyKey();return api<AIAction>('/api/v1/ai/actions:plan',{method:'POST',headers:{'Idempotency-Key':key},body:JSON.stringify({workbook_id:workbookId,sheet_id:sheetId,range:selectionRange,request:request.trim(),mode,base_version:baseVersion,idempotency_key:key,client_id:collaborationClientId()})})},
    onSuccess:async item=>{setAction(item);await refresh()},
  })
  const approve=useMutation({
    mutationFn:(item:AIAction)=>api<AIExecutionResult>(`/api/v1/ai/actions/${item.id}:approve`,{method:'POST',body:JSON.stringify({idempotency_key:newIdempotencyKey(),client_id:collaborationClientId(),expected_revision:item.revision})}),
    onSuccess:async result=>{setAction(result.action);onExecuted(result);await refresh()},
  })
  const undo=useMutation({
    mutationFn:(item:AIAction)=>api<AIExecutionResult>(`/api/v1/ai/actions/${item.id}:undo`,{method:'POST',body:JSON.stringify({idempotency_key:newIdempotencyKey(),client_id:collaborationClientId(),expected_revision:item.revision})}),
    onSuccess:async result=>{setAction(result.action);onExecuted(result);await refresh()},
  })
  const submit=(event:FormEvent)=>{event.preventDefault();if(request.trim()&&config.data?.enabled)plan.mutate()}
  const choose=(nextMode:AIAction['mode'],text:string)=>{setMode(nextMode);setRequest(text);setAction(undefined)}
  const loadAction=async(id:string)=>setAction(await api<AIAction>(`/api/v1/ai/actions/${id}`))
  const error=plan.error||approve.error||undo.error
  return <aside className="ai-panel managed-ai-panel" aria-label="AI 도우미 패널">
    <div className="ai-panel-head"><span><Bot/> AI 도우미</span><button onClick={onClose} aria-label="AI 도우미 닫기">×</button></div>
    <div className="ai-scroll">
      {config.isLoading&&<div className="ai-state"><RefreshCw className="spin"/>AI 설정을 확인하는 중…</div>}
      {config.isError&&<div className="ai-state error"><AlertTriangle/>AI 설정을 불러오지 못했습니다.</div>}
      {config.data&&!config.data.enabled&&<div className="ai-disabled"><Bot/><strong>AI가 아직 비활성화되어 있습니다.</strong><p>관리자가 사내 LLM Gateway를 검증하고 활성화하면 선택 범위 기반 계획을 사용할 수 있습니다.</p><a href="/admin?tab=settings">관리자 AI 설정 열기</a></div>}
      {config.data?.enabled&&<>
        <div className="ai-scope"><ShieldCheck/><div><strong>{selectionRange}만 모델에 전달</strong><small>최대 {config.data.max_input_cells.toLocaleString()}셀 · 모델 {config.data.model}</small></div></div>
        {!action&&<>
          <div className="ai-intro"><div className="ai-glyph"><Sparkles/></div><h3>안전한 AI 계획 만들기</h3><p>계획과 셀별 변경을 먼저 확인한 뒤 명시적으로 승인합니다.</p></div>
          <div className="suggestion-list"><button onClick={()=>choose('formula','선택 범위의 빈 열에 각 행의 합계를 계산하는 수식을 제안해줘')}>빈 열에 행별 합계 수식</button><button onClick={()=>choose('explain','선택한 수식의 계산 방식과 참조 범위를 설명해줘')}>선택 수식 설명</button><button onClick={()=>choose('fix','선택한 수식의 오류 원인을 찾아 수정안을 제안해줘')}>수식 오류 수정안</button></div>
        </>}
        {action&&<ActionPreview action={action} onApprove={()=>approve.mutate(action)} onUndo={()=>undo.mutate(action)} pending={approve.isPending||undo.isPending} onNew={()=>setAction(undefined)}/>}
        {error&&<div className="ai-error" role="alert"><AlertTriangle/>{error instanceof Error?error.message:'AI 작업을 처리하지 못했습니다.'}</div>}
        {!action&&<form className="ai-composer" onSubmit={submit}><label>작업 유형<select aria-label="AI 작업 유형" value={mode} onChange={event=>setMode(event.target.value as AIAction['mode'])}><option value="formula">수식 생성</option><option value="explain">수식 설명</option><option value="fix">수식 오류 수정</option></select></label><textarea aria-label="AI 요청" value={request} onChange={event=>setRequest(event.target.value)} maxLength={4000} placeholder="선택한 범위에 수행할 작업을 입력하세요…"/><button className="primary" disabled={!request.trim()||plan.isPending}>{plan.isPending?<RefreshCw className="spin"/>:<Send/>} {plan.isPending?'계획 생성 중':'계획 미리보기'}</button><small><Eye/> 승인 전에는 워크북을 변경하지 않습니다.</small></form>}
        {(recent.data?.items.length??0)>0&&<div className="ai-history"><div><Clock3/><strong>최근 AI 작업</strong></div>{recent.data?.items.slice(0,5).map(item=><button key={item.id} onClick={()=>void loadAction(item.id)}><span>{modeLabel[item.mode]} · {item.range}</span><small>{statusLabel(item.status)} · {new Date(item.created_at).toLocaleString('ko-KR')}</small></button>)}</div>}
      </>}
    </div>
  </aside>
}

function ActionPreview({action,onApprove,onUndo,pending,onNew}:{action:AIAction;onApprove:()=>void;onUndo:()=>void;pending:boolean;onNew:()=>void}){
  const readOnly=action.mode==='explain'
  return <div className="ai-action-preview">
    <div className="ai-action-title"><div><span>{modeLabel[action.mode]}</span><strong>{action.summary}</strong></div><em className={`ai-status ${action.status}`}>{statusLabel(action.status)}</em></div>
    <p>{action.explanation}</p>
    {action.changes.length>0&&<div className="ai-change-list">{action.changes.map(change=><article key={`${change.row}:${change.column}`}><strong>{change.address}</strong><div><small>현재</small><code>{snapshotText(change.before)}</code></div><span>→</span><div><small>제안</small><code>{snapshotText(change.after)}</code></div></article>)}</div>}
    <div className="ai-plan-meta"><span>기준 버전 v{action.base_version}</span><span>{action.changes.length}셀 변경</span><span>{action.model}</span></div>
    {action.status==='failed'&&<div className="ai-error"><AlertTriangle/>{action.error_message||'작업이 실패했습니다.'}</div>}
    {action.status==='planned'&&!readOnly&&<div className="ai-approval"><p><ShieldCheck/> 위 변경만 하나의 원자적 작업으로 적용됩니다.</p><button className="primary" onClick={onApprove} disabled={pending}><Check/> 검토한 계획 승인</button></div>}
    {(action.status==='completed'||(action.status==='planned'&&readOnly))&&<div className="ai-explanation-done"><Check/> 읽기 전용 설명이며 워크북 변경이 없습니다.</div>}
    {(action.status==='applying'||action.status==='undoing')&&<div className="ai-state"><RefreshCw className="spin"/>서버 작업을 마무리하는 중…</div>}
    {action.status==='applied'&&<div className="ai-applied"><Check/><div><strong>승인한 변경이 적용되었습니다.</strong><small>서버 버전 v{action.operation?.server_version}</small></div><button onClick={onUndo} disabled={pending}><RotateCcw/> Undo</button></div>}
    {action.status==='undone'&&<div className="ai-explanation-done"><RotateCcw/> AI 변경을 새 서버 버전으로 되돌렸습니다.</div>}
    <button className="ai-new-plan" onClick={onNew}>새 요청 작성</button>
  </div>
}

function statusLabel(status:AIAction['status']){return {planned:'승인 대기',completed:'설명 완료',applying:'적용 중',applied:'적용됨',undoing:'취소 중',undone:'취소됨',failed:'실패'}[status]}
