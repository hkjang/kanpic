import { Check, ChevronLeft, ChevronRight, Cloud, CloudOff, Copy, Eye, EyeOff, LayoutList, MoreHorizontal, Palette, Plus, Save, SquareArrowOutUpRight, Trash2 } from 'lucide-react'
import { useRef, useState } from 'react'
import type { Sheet } from '../types'

const colors=['','#ef4444','#f59e0b','#22c55e','#06b6d4','#3b82f6','#8b5cf6','#ec4899']

type Props={
  sheets:Sheet[]
  activeSheetId:string
  saveState:'saved'|'saving'|'offline'|'conflict'|'error'
  saveLabel:string
  readOnly?:boolean
  onStatusClick?:()=>void
  onSelect:(sheet:Sheet)=>void
  onCreate:()=>Promise<void>
  onRename:(sheet:Sheet,name:string)=>Promise<void>
  onDuplicate:(sheet:Sheet)=>Promise<void>
  onMove:(sheet:Sheet,position:number)=>Promise<void>
  onColor:(sheet:Sheet,color:string)=>Promise<void>
  onHidden:(sheet:Sheet,hidden:boolean)=>Promise<void>
  onDelete:(sheet:Sheet)=>Promise<void>
  onCopyTo?:(sheet:Sheet)=>void
  onManage?:()=>void
}

export function SheetTabs({sheets,activeSheetId,saveState,saveLabel,readOnly=false,onStatusClick,onSelect,onCreate,onRename,onDuplicate,onMove,onColor,onHidden,onDelete,onCopyTo,onManage}:Props){
  const scroller=useRef<HTMLDivElement>(null)
  const dragged=useRef<string|undefined>(undefined)
  const [menu,setMenu]=useState<string>(),[renaming,setRenaming]=useState<string>(),[name,setName]=useState(''),[pending,setPending]=useState(false),[error,setError]=useState(''),[dropTarget,setDropTarget]=useState<string>(),[hiddenOpen,setHiddenOpen]=useState(false)
  const run=async(action:()=>Promise<void>)=>{setPending(true);setError('');try{await action();setMenu(undefined);setRenaming(undefined)}catch(reason){setError(reason instanceof Error?reason.message:'시트 작업을 완료하지 못했습니다.')}finally{setPending(false)}}
  const startRename=(sheet:Sheet)=>{setMenu(sheet.id);setRenaming(sheet.id);setName(sheet.name);setError('')}
  const submitRename=(sheet:Sheet)=>{const next=name.trim();if(!next||next===sheet.name){setRenaming(undefined);return}void run(()=>onRename(sheet,next))}
  const scroll=(direction:number)=>scroller.current?.scrollBy({left:direction*220,behavior:'smooth'})
  const visible=sheets.filter(sheet=>!sheet.hidden)
  const hidden=sheets.filter(sheet=>sheet.hidden)
  const menuSheet=sheets.find(sheet=>sheet.id===menu)
  // Dropping a tab moves it in front of the tab it was dropped on, matching the
  // drag behaviour of a spreadsheet tab strip.
  const drop=(target:Sheet)=>{
    const sourceID=dragged.current
    dragged.current=undefined
    setDropTarget(undefined)
    if(!sourceID||sourceID===target.id)return
    const source=sheets.find(sheet=>sheet.id===sourceID)
    if(!source)return
    void run(()=>onMove(source,target.position))
  }
  return <div className="sheet-tabs">
    <button className="tab-nav" onClick={()=>scroll(-1)} aria-label="시트 탭 왼쪽으로"><ChevronLeft/></button>
    <button className="tab-nav" onClick={()=>scroll(1)} aria-label="시트 탭 오른쪽으로"><ChevronRight/></button>
    {!readOnly&&<button className="tab-add" disabled={pending} onClick={()=>run(onCreate)} aria-label="시트 추가"><Plus/></button>}
    {onManage&&<button className="tab-nav" onClick={onManage} aria-label="모든 시트 관리" title="모든 시트 관리"><LayoutList/></button>}
    <div className="sheet-tab-scroller" ref={scroller}>
      {visible.map(sheet=><div className={`sheet-tab-wrap${dropTarget===sheet.id?' drop-target':''}`} key={sheet.id}
        draggable={!readOnly&&renaming!==sheet.id}
        onDragStart={()=>{dragged.current=sheet.id}}
        onDragOver={event=>{if(dragged.current&&dragged.current!==sheet.id){event.preventDefault();setDropTarget(sheet.id)}}}
        onDragLeave={()=>setDropTarget(current=>current===sheet.id?undefined:current)}
        onDrop={event=>{event.preventDefault();drop(sheet)}}
        onDragEnd={()=>{dragged.current=undefined;setDropTarget(undefined)}}>
        {renaming===sheet.id?<form className="sheet-tab-rename" onSubmit={event=>{event.preventDefault();submitRename(sheet)}}><input autoFocus value={name} maxLength={100} aria-label="시트 이름" onChange={event=>setName(event.target.value)} onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();setRenaming(undefined)}}}/><button disabled={pending} aria-label="시트 이름 저장"><Check/></button></form>:<button className={`sheet-tab-main ${sheet.id===activeSheetId?'active':''}`} onClick={()=>onSelect(sheet)} onDoubleClick={()=>{if(!readOnly)startRename(sheet)}}><i className={sheet.color?'':'empty'} style={sheet.color?{background:sheet.color}:undefined}/><span>{sheet.name}</span></button>}
        {sheet.id===activeSheetId&&renaming!==sheet.id&&<button className="sheet-tab-menu-trigger" aria-label={`${sheet.name} 시트 메뉴`} onClick={()=>setMenu(current=>current===sheet.id?undefined:sheet.id)}><MoreHorizontal/></button>}
      </div>)}
    </div>
    {hidden.length>0&&<div className="sheet-hidden-group">
      <button className="sheet-hidden-trigger" aria-expanded={hiddenOpen} onClick={()=>setHiddenOpen(current=>!current)}><EyeOff/> 숨긴 시트 {hidden.length}</button>
      {hiddenOpen&&<div className="sheet-hidden-list" role="menu">
        {hidden.map(sheet=><div key={sheet.id}>
          <span><i className={sheet.color?'':'empty'} style={sheet.color?{background:sheet.color}:undefined}/>{sheet.name}</span>
          <button disabled={pending||readOnly} aria-label={`${sheet.name} 숨김 해제`} onClick={()=>void run(async()=>{await onHidden(sheet,false);setHiddenOpen(false)})}><Eye/> 표시</button>
        </div>)}
      </div>}
    </div>}
    {menuSheet&&renaming!==menuSheet.id&&<div className="sheet-tab-menu" role="menu">
      <strong>{menuSheet.name}</strong>
      <button role="menuitem" disabled={readOnly} onClick={()=>startRename(menuSheet)}><Save/> 이름 변경</button>
      <button role="menuitem" disabled={pending||readOnly} onClick={()=>run(()=>onDuplicate(menuSheet))}><Copy/> 복제</button>
      {onCopyTo&&<button role="menuitem" disabled={pending} onClick={()=>{setMenu(undefined);onCopyTo(menuSheet)}}><SquareArrowOutUpRight/> 다른 워크북으로 복사</button>}
      <div className="sheet-move-actions"><button disabled={pending||readOnly||menuSheet.position===0} onClick={()=>run(()=>onMove(menuSheet,menuSheet.position-1))}><ChevronLeft/> 왼쪽</button><button disabled={pending||readOnly||menuSheet.position===sheets.length-1} onClick={()=>run(()=>onMove(menuSheet,menuSheet.position+1))}>오른쪽 <ChevronRight/></button></div>
      <div className="sheet-color-title"><Palette/> 탭 색상</div><div className="sheet-color-list">{colors.map((color,index)=><button key={color||'none'} disabled={readOnly} className={menuSheet.color===color?'selected':''} aria-label={color?`시트 색상 ${color}`:'시트 색상 없음'} onClick={()=>run(()=>onColor(menuSheet,color))}><i className={index===0?'none':''} style={color?{background:color}:undefined}/></button>)}</div>
      <button role="menuitem" disabled={pending||readOnly||visible.length===1} title={visible.length===1?'표시된 시트가 하나뿐입니다':undefined} onClick={()=>run(()=>onHidden(menuSheet,true))}><EyeOff/> 시트 숨기기</button>
      <button role="menuitem" className="danger" disabled={pending||readOnly||sheets.length===1} onClick={()=>{if(confirm(`'${menuSheet.name}' 시트와 모든 셀을 삭제할까요?`))void run(()=>onDelete(menuSheet))}}><Trash2/> 삭제</button>
      {error&&<small className="sheet-menu-error">{error}</small>}
    </div>}
    {error&&<span className="sheet-tabs-error">{error}</span>}
    <button className="sheet-status" disabled={!onStatusClick} onClick={onStatusClick}>{saveState==='offline'?<CloudOff/>:<Cloud/>} {saveLabel}</button>
  </div>
}
