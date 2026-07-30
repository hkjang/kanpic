import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { Clock3, Copy, FilePlus2, FileUp, Grid2X2, MoreHorizontal, Pencil, Plus, Star, Trash2, UploadCloud } from 'lucide-react'
import { AppHeader } from '../components/AppHeader'
import { api, newIdempotencyKey } from '../lib/api'
import type { BuildInfo, Session, Workbook } from '../types'

export function HomePage({build,session}:{build?:BuildInfo;session?:Session}) {
  const client=useQueryClient()
  const inputRef=useRef<HTMLInputElement>(null)
  const [importFile,setImportFile]=useState<File>()
  const [preview,setPreview]=useState<{format:string;total_cells:number;sheets:Array<{name:string;rows:number;columns:number;non_empty_cells:number}>;warnings:string[]} >()
  const [importing,setImporting]=useState(false)
  const [filter,setFilter]=useState<'recent'|'favorite'>('recent')
  const [menuID,setMenuID]=useState<string>()
  const [renameTarget,setRenameTarget]=useState<Workbook>()
  const [renameTitle,setRenameTitle]=useState('')
  const workbooks=useQuery({queryKey:['workbooks'],queryFn:()=>api<{items:Workbook[]}>('/api/v1/workbooks')})
  const create=useMutation({mutationFn:(requestedTitle?:string)=>api<Workbook>('/api/v1/workbooks',{method:'POST',body:JSON.stringify({title:requestedTitle?.trim()||'제목 없는 워크북',workspace_id:'default'})}),onSuccess:(wb)=>{client.invalidateQueries({queryKey:['workbooks']});window.location.href=`/workbooks/${wb.id}`}})
  const update=useMutation({mutationFn:({id,input}:{id:string;input:Record<string,unknown>})=>api<Workbook>(`/api/v1/workbooks/${id}`,{method:'PATCH',body:JSON.stringify(input)}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const duplicate=useMutation({mutationFn:(workbook:Workbook)=>api<Workbook>(`/api/v1/workbooks/${workbook.id}/duplicate`,{method:'POST',body:JSON.stringify({title:`${workbook.title} 복사본`})}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const remove=useMutation({mutationFn:(workbook:Workbook)=>api(`/api/v1/workbooks/${workbook.id}`,{method:'DELETE'}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const chooseImport=async(file?:File)=>{if(!file)return;setImportFile(file);const form=new FormData();form.append('file',file);const result=await api<{format:string;total_cells:number;sheets:Array<{name:string;rows:number;columns:number;non_empty_cells:number}>;warnings:string[]}>('/api/v1/imports:preview',{method:'POST',body:form});setPreview(result)}
  const executeImport=async()=>{if(!importFile)return;setImporting(true);try{const form=new FormData();form.append('file',importFile);form.append('workspace_id','default');const created=await api<Workbook>('/api/v1/imports',{method:'POST',body:form,headers:{'Idempotency-Key':newIdempotencyKey()}});window.location.href=`/workbooks/${created.id}`}finally{setImporting(false)}}
  const openRename=(workbook:Workbook)=>{setMenuID(undefined);setRenameTarget(workbook);setRenameTitle(workbook.title)}
  const rename=async()=>{if(!renameTarget||!renameTitle.trim())return;await update.mutateAsync({id:renameTarget.id,input:{title:renameTitle.trim()}});setRenameTarget(undefined)}
  const copyWorkbook=async(workbook:Workbook)=>{setMenuID(undefined);await duplicate.mutateAsync(workbook)}
  const deleteWorkbook=async(workbook:Workbook)=>{setMenuID(undefined);if(!confirm(`'${workbook.title}' 워크북과 모든 시트를 삭제할까요?`))return;await remove.mutateAsync(workbook)}
  const visibleWorkbooks=(workbooks.data?.items??[]).filter(workbook=>filter==='recent'||workbook.favorite)

  return <div className="page-shell"><AppHeader build={build} session={session}/><main className="home-content">
    <section className="home-title"><div><span className="eyebrow">WORKSPACE</span><h1>좋은 아침이에요.</h1><p>오늘도 데이터에서 더 나은 답을 만들어 보세요.</p></div><div className="home-title-actions"><input ref={inputRef} type="file" hidden accept=".csv,.tsv,.xlsx" onChange={event=>chooseImport(event.target.files?.[0])}/><button className="secondary" onClick={()=>inputRef.current?.click()}><FileUp size={18}/> 파일 가져오기</button><button className="primary" onClick={()=>create.mutate(undefined)}><Plus size={18}/> 새 워크북</button></div></section>
    <section className="quick-start"><div className="section-heading"><h2>빠른 시작</h2></div><div className="template-row">
      <button className="template blank" onClick={()=>create.mutate(undefined)}><span><FilePlus2/></span><strong>빈 워크북</strong><small>새로운 데이터 작업 시작</small></button>
      {['프로젝트 현황','월간 매출 분석','업무 요청 관리'].map((name,index)=><button className={`template template-${index}`} key={name} onClick={()=>create.mutate(name)}><span><Grid2X2/></span><strong>{name}</strong><small>kanpic 기본 템플릿</small></button>)}
    </div></section>
    <section className="recent-section"><div className="section-heading"><h2>최근 워크북</h2><div className="segmented"><button className={filter==='recent'?'active':''} onClick={()=>setFilter('recent')}><Clock3/> 최근</button><button className={filter==='favorite'?'active':''} onClick={()=>setFilter('favorite')}><Star/> 즐겨찾기</button></div></div>
      {workbooks.isLoading?<div className="loading-card">워크북을 불러오는 중…</div>:visibleWorkbooks.length===0?<div className="empty-state"><FilePlus2/><h3>{filter==='favorite'?'즐겨찾기한 워크북이 없습니다':'첫 워크북을 만들어 보세요'}</h3><p>{filter==='favorite'?'워크북 메뉴에서 즐겨찾기에 추가할 수 있습니다.':'셀 편집 내용은 자동으로 안전하게 저장됩니다.'}</p></div>:<div className="workbook-grid">{visibleWorkbooks.map(workbook=><article className={`workbook-card ${menuID===workbook.id?'menu-open':''}`} key={workbook.id}>
        <a href={`/workbooks/${workbook.id}`} className="workbook-preview"><Grid2X2/><span>{workbook.sheets.length} sheets</span>{workbook.favorite&&<Star className="favorite-star" fill="currentColor"/>}</a>
        <div className="workbook-meta"><a href={`/workbooks/${workbook.id}`}><strong>{workbook.title}</strong><small>{new Date(workbook.updated_at).toLocaleString('ko-KR')} 수정</small></a><button aria-label={`${workbook.title} 더보기`} aria-expanded={menuID===workbook.id} onClick={()=>setMenuID(current=>current===workbook.id?undefined:workbook.id)}><MoreHorizontal/></button>{menuID===workbook.id&&<div className="workbook-menu" role="menu">
          <button role="menuitem" onClick={()=>{setMenuID(undefined);update.mutate({id:workbook.id,input:{favorite:!workbook.favorite}})}}><Star fill={workbook.favorite?'currentColor':'none'}/>{workbook.favorite?'즐겨찾기 해제':'즐겨찾기'}</button>
          <button role="menuitem" onClick={()=>openRename(workbook)}><Pencil/> 이름 변경</button>
          <button role="menuitem" onClick={()=>void copyWorkbook(workbook)}><Copy/> 복제</button>
          <button role="menuitem" className="danger" onClick={()=>void deleteWorkbook(workbook)}><Trash2/> 삭제</button>
        </div>}</div>
      </article>)}</div>}
    </section>
    {renameTarget&&<div className="modal-backdrop"><div className="modal"><h2>워크북 이름 변경</h2><label>워크북 이름<input aria-label="워크북 이름" autoFocus value={renameTitle} onChange={event=>setRenameTitle(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')void rename()}}/></label><div className="modal-actions"><button className="secondary" onClick={()=>setRenameTarget(undefined)}>취소</button><button className="primary" aria-label="워크북 이름 저장" disabled={!renameTitle.trim()||update.isPending} onClick={()=>void rename()}>저장</button></div></div></div>}
    {preview&&importFile&&<div className="modal-backdrop"><div className="modal import-modal"><div className="import-preview-icon"><UploadCloud/></div><h2>{importFile.name}</h2><p>{preview.format.toUpperCase()} · 비어 있지 않은 셀 {preview.total_cells.toLocaleString()}개</p><div className="import-sheet-list">{preview.sheets.map(sheet=><div key={sheet.name}><Grid2X2/><div><strong>{sheet.name}</strong><small>{sheet.rows.toLocaleString()}행 × {sheet.columns.toLocaleString()}열 · {sheet.non_empty_cells.toLocaleString()}개 셀</small></div></div>)}</div>{preview.warnings.length>0&&<div className="import-warnings">{preview.warnings.map(warning=><span key={warning}>{warning}</span>)}</div>}<div className="modal-actions"><button className="secondary" onClick={()=>{setPreview(undefined);setImportFile(undefined)}}>취소</button><button className="primary" disabled={importing} onClick={executeImport}>{importing?'가져오는 중…':'워크북으로 가져오기'}</button></div></div></div>}
  </main></div>
}
