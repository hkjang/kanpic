import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { Building2, ChevronRight, Clock3, Copy, FilePlus2, FileUp, Grid2X2, Link as LinkIcon, Lock, MoreHorizontal, Pencil, Plus, RotateCcw, Share2, SquareArrowOutUpRight, Star, Trash, Trash2, UploadCloud, Users } from 'lucide-react'
import { AppHeader } from '../components/AppHeader'
import { api, newIdempotencyKey } from '../lib/api'
import type { BuildInfo, Session, ShareRole, Workbook } from '../types'
import { ShareDialog } from '../components/ShareDialog'
import { useUserDirectory, userLabel, userTooltip } from '../state/directory'
import { useDialog } from '../lib/useDialog'
import { ContextMenu,type MenuItem } from '../components/ContextMenu'
import { TemplateGallery,useTemplates,type WorkbookTemplate } from '../components/TemplateGallery'

const ROLE_CHIP:Record<ShareRole,string>={owner:'소유자',editor:'편집자',commenter:'댓글 작성자',viewer:'뷰어'}

/** Shows how the workbook reached this user, mirroring the Sheets home list. */
function accessChip(workbook:Workbook){
  if(!workbook.access_role||workbook.access_role==='owner')return workbook.link_access&&workbook.link_access!=='restricted'
    ?<span className="workbook-access shared" title="링크 액세스가 켜져 있습니다"><Users/> 공유 중</span>
    :null
  const source=workbook.access_source==='department'?<Building2/>:workbook.access_source==='link'?<Users/>:<Share2/>
  return <span className={`workbook-access ${workbook.access_role}`} title={`${ROLE_CHIP[workbook.access_role]} 권한`}>{source} {ROLE_CHIP[workbook.access_role]}</span>
}

/** Renaming a workbook shares the standard dialog behaviour: Escape, focus trap and focus restore. */
function RenameWorkbookDialog({title,pending,onTitle,onClose,onSave}:{title:string;pending:boolean;onTitle:(value:string)=>void;onClose:()=>void;onSave:()=>void}){
  const dialog=useDialog<HTMLElement>(onClose)
  return <div className="modal-backdrop" role="presentation" onMouseDown={event=>{if(event.target===event.currentTarget)onClose()}}>
    <div className="modal" ref={dialog as React.RefObject<any>} role="dialog" aria-modal="true" aria-label="워크북 이름 변경">
      <h2>워크북 이름 변경</h2>
      <label>워크북 이름<input aria-label="워크북 이름" autoFocus value={title} onChange={event=>onTitle(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')onSave()}}/></label>
      <div className="modal-actions"><button className="secondary" onClick={onClose}>취소</button><button className="primary" aria-label="워크북 이름 저장" disabled={!title.trim()||pending} onClick={onSave}>저장</button></div>
    </div>
  </div>
}

export function HomePage({build,session}:{build?:BuildInfo;session?:Session}) {
  const client=useQueryClient()
  const inputRef=useRef<HTMLInputElement>(null)
  const [importFile,setImportFile]=useState<File>()
  const [preview,setPreview]=useState<{format:string;total_cells:number;sheets:Array<{name:string;rows:number;columns:number;non_empty_cells:number}>;warnings:string[]} >()
  const [importing,setImporting]=useState(false)
  const [filter,setFilter]=useState<'recent'|'favorite'|'owned'|'shared'|'trash'>('recent')
  const [shareTarget,setShareTarget]=useState<Workbook>()
  const [cardMenu,setCardMenu]=useState<{x:number;y:number;items:MenuItem[];label:string}>()
  const [renameTarget,setRenameTarget]=useState<Workbook>()
  const [renameTitle,setRenameTitle]=useState('')
  const workbooks=useQuery({queryKey:['workbooks'],queryFn:()=>api<{items:Workbook[]}>('/api/v1/workbooks')})
  const create=useMutation({
    mutationFn:(input?:{title?:string;templateId?:string})=>api<Workbook>('/api/v1/workbooks',{method:'POST',body:JSON.stringify({
      title:input?.title?.trim()||(input?.templateId?undefined:'제목 없는 워크북'),workspace_id:'default',template_id:input?.templateId,
    })}),
    onSuccess:(wb)=>{client.invalidateQueries({queryKey:['workbooks']});window.location.href=`/workbooks/${wb.id}`},
    onError:(error)=>alert(error instanceof Error?error.message:'워크북을 만들지 못했습니다.'),
  })
  const [galleryOpen,setGalleryOpen]=useState(false)
  const templates=useTemplates()
  const featured=['monthly-sales','project-status','invoice'].map(id=>templates.find(item=>item.id===id)).filter(Boolean) as WorkbookTemplate[]
  const useTemplate=(template:WorkbookTemplate)=>create.mutate({templateId:template.id})
  const update=useMutation({mutationFn:({id,input}:{id:string;input:Record<string,unknown>})=>api<Workbook>(`/api/v1/workbooks/${id}`,{method:'PATCH',body:JSON.stringify(input)}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const favorite=useMutation({mutationFn:({id,value}:{id:string;value:boolean})=>api<Workbook>(`/api/v1/workbooks/${id}/favorite`,{method:'PUT',body:JSON.stringify({favorite:value})}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const trash=useQuery({queryKey:['workbook-trash'],queryFn:()=>api<{items:Workbook[]}>('/api/v1/workbooks/trash'),enabled:filter==='trash'})
  const restore=useMutation({mutationFn:(workbook:Workbook)=>api<Workbook>(`/api/v1/workbooks/${workbook.id}/restore`,{method:'POST'}),onSuccess:()=>{client.invalidateQueries({queryKey:['workbook-trash']});client.invalidateQueries({queryKey:['workbooks']})}})
  const purge=useMutation({mutationFn:(workbook:Workbook)=>api(`/api/v1/workbooks/${workbook.id}/purge`,{method:'DELETE'}),onSuccess:()=>client.invalidateQueries({queryKey:['workbook-trash']})})
  const duplicate=useMutation({mutationFn:(workbook:Workbook)=>api<Workbook>(`/api/v1/workbooks/${workbook.id}/duplicate`,{method:'POST',body:JSON.stringify({title:`${workbook.title} 복사본`})}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const remove=useMutation({mutationFn:(workbook:Workbook)=>api(`/api/v1/workbooks/${workbook.id}`,{method:'DELETE'}),onSuccess:()=>client.invalidateQueries({queryKey:['workbooks']})})
  const chooseImport=async(file?:File)=>{if(!file)return;setImportFile(file);const form=new FormData();form.append('file',file);const result=await api<{format:string;total_cells:number;sheets:Array<{name:string;rows:number;columns:number;non_empty_cells:number}>;warnings:string[]}>('/api/v1/imports:preview',{method:'POST',body:form});setPreview(result)}
  const executeImport=async()=>{if(!importFile)return;setImporting(true);try{const form=new FormData();form.append('file',importFile);form.append('workspace_id','default');const created=await api<Workbook>('/api/v1/imports',{method:'POST',body:form,headers:{'Idempotency-Key':newIdempotencyKey()}});window.location.href=`/workbooks/${created.id}`}finally{setImporting(false)}}
  const openRename=(workbook:Workbook)=>{setRenameTarget(workbook);setRenameTitle(workbook.title)}
  const rename=async()=>{if(!renameTarget||!renameTitle.trim())return;await update.mutateAsync({id:renameTarget.id,input:{title:renameTitle.trim()}});setRenameTarget(undefined)}
  const copyWorkbook=async(workbook:Workbook)=>{await duplicate.mutateAsync(workbook)}
  const deleteWorkbook=async(workbook:Workbook)=>{if(!confirm(`'${workbook.title}' 워크북과 모든 시트를 삭제할까요?`))return;await remove.mutateAsync(workbook)}
  const shareWorkbook=(workbook:Workbook)=>{setShareTarget(workbook)}
  const cardMenuItems=(workbook:Workbook):MenuItem[]=>[
    {kind:'label',label:workbook.title},
    {kind:'item',label:'열기',icon:<Grid2X2/>,onSelect:()=>{window.location.href=`/workbooks/${workbook.id}`}},
    {kind:'item',label:'새 탭에서 열기',icon:<SquareArrowOutUpRight/>,onSelect:()=>window.open(`/workbooks/${workbook.id}`,'_blank','noopener')},
    {kind:'separator'},
    {kind:'item',label:'공유…',icon:<Share2/>,onSelect:()=>shareWorkbook(workbook)},
    {kind:'item',label:workbook.favorite?'즐겨찾기 해제':'즐겨찾기에 추가',icon:<Star/>,onSelect:()=>favorite.mutate({id:workbook.id,value:!workbook.favorite})},
    {kind:'item',label:'이름 변경',icon:<Pencil/>,disabled:workbook.access_role!=='owner'&&workbook.access_role!=='editor',onSelect:()=>openRename(workbook)},
    {kind:'item',label:'복제',icon:<Copy/>,onSelect:()=>void copyWorkbook(workbook)},
    {kind:'item',label:'링크 복사',icon:<LinkIcon/>,onSelect:()=>{
      const link=`${window.location.origin}/workbooks/${workbook.id}`
      void navigator.clipboard?.writeText(link).catch(()=>window.prompt('링크를 복사하세요.',link))
    }},
    {kind:'separator'},
    {kind:'item',label:'휴지통으로 이동',icon:<Trash2/>,danger:true,disabled:workbook.access_role!=='owner',onSelect:()=>void deleteWorkbook(workbook)},
  ]
  const openCardMenu=(workbook:Workbook,point:{clientX:number;clientY:number})=>{
    setCardMenu({x:point.clientX,y:point.clientY,items:cardMenuItems(workbook),label:`${workbook.title} 메뉴`})
  }
  const directory=useUserDirectory((workbooks.data?.items??[]).map(item=>item.owner_id))
  const visibleWorkbooks=(workbooks.data?.items??[]).filter(workbook=>{
    if(filter==='favorite')return workbook.favorite
    if(filter==='owned')return workbook.access_role==='owner'
    if(filter==='shared')return workbook.access_role!=='owner'
    return true
  })

  return <div className="page-shell"><AppHeader build={build} session={session}/><main className="home-content">
    <section className="home-title"><div><span className="eyebrow">WORKSPACE</span><h1>좋은 아침이에요.</h1><p>오늘도 데이터에서 더 나은 답을 만들어 보세요.</p></div><div className="home-title-actions"><input ref={inputRef} type="file" hidden accept=".csv,.tsv,.xlsx" onChange={event=>chooseImport(event.target.files?.[0])}/><button className="secondary" onClick={()=>inputRef.current?.click()}><FileUp size={18}/> 파일 가져오기</button><button className="primary" onClick={()=>create.mutate(undefined)}><Plus size={18}/> 새 워크북</button></div></section>
    <section className="quick-start"><div className="section-heading"><h2>빠른 시작</h2><button className="link-button" onClick={()=>setGalleryOpen(true)}>템플릿 갤러리{templates.length>0?` (${templates.length})`:''} <ChevronRight size={14}/></button></div><div className="template-row">
      <button className="template blank" onClick={()=>create.mutate(undefined)}><span><FilePlus2/></span><strong>빈 워크북</strong><small>새로운 데이터 작업 시작</small></button>
      {featured.map((template,index)=><button className={`template template-${index}`} key={template.id} disabled={create.isPending} onClick={()=>useTemplate(template)}><span><Grid2X2/></span><strong>{template.name}</strong><small>{template.summary}</small></button>)}
      {featured.length===0&&['월간 매출 분석','프로젝트 현황','거래명세서'].map((name,index)=><button className={`template template-${index}`} key={name} disabled><span><Grid2X2/></span><strong>{name}</strong><small>템플릿을 불러오는 중…</small></button>)}
    </div></section>
    <section className="recent-section" onContextMenu={event=>{
      if(!(event.target as HTMLElement).closest('.workbook-card')){
        event.preventDefault()
        setCardMenu({x:event.clientX,y:event.clientY,label:'워크북 목록 메뉴',items:[
          {kind:'item',label:'새 워크북',icon:<Plus/>,onSelect:()=>create.mutate(undefined)},
          {kind:'item',label:'파일 가져오기',icon:<FileUp/>,onSelect:()=>inputRef.current?.click()},
          {kind:'item',label:'휴지통 보기',icon:<Trash/>,onSelect:()=>setFilter('trash')},
        ]})
      }
    }}><div className="section-heading"><h2>최근 워크북</h2><div className="segmented"><button className={filter==='recent'?'active':''} onClick={()=>setFilter('recent')}><Clock3/> 최근</button><button className={filter==='owned'?'active':''} onClick={()=>setFilter('owned')}><Lock/> 내 소유</button><button className={filter==='shared'?'active':''} onClick={()=>setFilter('shared')}><Users/> 나와 공유됨</button><button className={filter==='favorite'?'active':''} onClick={()=>setFilter('favorite')}><Star/> 즐겨찾기</button><button className={filter==='trash'?'active':''} onClick={()=>setFilter('trash')}><Trash/> 휴지통</button></div></div>
      {filter==='trash'
        ?trash.isLoading?<div className="loading-card">휴지통을 불러오는 중…</div>
          :(trash.data?.items??[]).length===0?<div className="empty-state"><Trash/><h3>휴지통이 비어 있습니다</h3><p>삭제한 워크북은 여기에서 복원하거나 완전히 지울 수 있습니다.</p></div>
          :<div className="trash-list">{(trash.data?.items??[]).map(item=><article className="trash-row" key={item.id}>
            <span className="trash-info"><strong>{item.title}</strong><small>{item.deleted_at?`${new Date(item.deleted_at).toLocaleString('ko-KR')} 삭제`:'삭제됨'}{item.deleted_by?` · ${item.deleted_by}`:''}</small></span>
            <button disabled={restore.isPending} onClick={()=>void restore.mutateAsync(item)}><RotateCcw/> 복원</button>
            <button className="danger" disabled={purge.isPending} onClick={()=>{if(confirm(`'${item.title}' 워크북을 완전히 삭제할까요? 이 작업은 되돌릴 수 없습니다.`))void purge.mutateAsync(item)}}><Trash2/> 완전 삭제</button>
          </article>)}</div>
        :workbooks.isLoading?<div className="loading-card">워크북을 불러오는 중…</div>:visibleWorkbooks.length===0?<div className="empty-state"><FilePlus2/><h3>{filter==='favorite'?'즐겨찾기한 워크북이 없습니다':filter==='shared'?'나와 공유된 워크북이 없습니다':filter==='owned'?'내가 소유한 워크북이 없습니다':'첫 워크북을 만들어 보세요'}</h3><p>{filter==='favorite'?'워크북 메뉴에서 즐겨찾기에 추가할 수 있습니다.':filter==='shared'?'동료가 사용자, 부서 또는 역할로 공유하면 여기에 표시됩니다.':'셀 편집 내용은 자동으로 안전하게 저장됩니다.'}</p></div>:<div className="workbook-grid">{visibleWorkbooks.map(workbook=><article className="workbook-card" key={workbook.id} onContextMenu={event=>{event.preventDefault();openCardMenu(workbook,event)}}>
        <a href={`/workbooks/${workbook.id}`} className="workbook-preview"><Grid2X2/><span>{workbook.sheets.length} sheets</span>{workbook.favorite&&<Star className="favorite-star" fill="currentColor"/>}</a>
        <div className="workbook-meta"><a href={`/workbooks/${workbook.id}`}><strong>{workbook.title}</strong><small>{new Date(workbook.updated_at).toLocaleString('ko-KR')} 수정{workbook.access_role&&workbook.access_role!=='owner'?` · ${userLabel(workbook.owner_id,directory)} 소유`:''}</small></a>{accessChip(workbook)}<button aria-label={`${workbook.title} 더보기`} aria-haspopup="menu" onClick={event=>{const rect=event.currentTarget.getBoundingClientRect();openCardMenu(workbook,{clientX:rect.right-8,clientY:rect.bottom})}}><MoreHorizontal/></button></div>
      </article>)}</div>}
    </section>
    {renameTarget&&<RenameWorkbookDialog title={renameTitle} pending={update.isPending} onTitle={setRenameTitle} onClose={()=>setRenameTarget(undefined)} onSave={()=>void rename()}/>}
    {preview&&importFile&&<div className="modal-backdrop"><div className="modal import-modal"><div className="import-preview-icon"><UploadCloud/></div><h2>{importFile.name}</h2><p>{preview.format.toUpperCase()} · 비어 있지 않은 셀 {preview.total_cells.toLocaleString()}개</p><div className="import-sheet-list">{preview.sheets.map(sheet=><div key={sheet.name}><Grid2X2/><div><strong>{sheet.name}</strong><small>{sheet.rows.toLocaleString()}행 × {sheet.columns.toLocaleString()}열 · {sheet.non_empty_cells.toLocaleString()}개 셀</small></div></div>)}</div>{preview.warnings.length>0&&<div className="import-warnings">{preview.warnings.map(warning=><span key={warning}>{warning}</span>)}</div>}<div className="modal-actions"><button className="secondary" onClick={()=>{setPreview(undefined);setImportFile(undefined)}}>취소</button><button className="primary" disabled={importing} onClick={executeImport}>{importing?'가져오는 중…':'워크북으로 가져오기'}</button></div></div></div>}
    {galleryOpen&&<TemplateGallery onClose={()=>setGalleryOpen(false)} onCreate={useTemplate} pending={create.isPending?create.variables?.templateId:undefined}/>}
    {cardMenu&&<ContextMenu x={cardMenu.x} y={cardMenu.y} items={cardMenu.items} label={cardMenu.label} onClose={()=>setCardMenu(undefined)}/>}
    {shareTarget&&<ShareDialog workbook={shareTarget} onClose={()=>setShareTarget(undefined)} onChanged={()=>{void client.invalidateQueries({queryKey:['workbooks']})}}/>}
  </main></div>
}
