import { Check, ChevronLeft, ChevronRight, Cloud, CloudOff, Copy, MoreHorizontal, Palette, Plus, Save, Trash2 } from 'lucide-react'
import { useRef, useState } from 'react'
import type { Sheet } from '../types'

const colors=['','#ef4444','#f59e0b','#22c55e','#06b6d4','#3b82f6','#8b5cf6','#ec4899']

type Props={
  sheets:Sheet[]
  activeSheetId:string
  saveState:'saved'|'saving'|'offline'|'conflict'|'error'
  saveLabel:string
  onStatusClick?:()=>void
  onSelect:(sheet:Sheet)=>void
  onCreate:()=>Promise<void>
  onRename:(sheet:Sheet,name:string)=>Promise<void>
  onDuplicate:(sheet:Sheet)=>Promise<void>
  onMove:(sheet:Sheet,position:number)=>Promise<void>
  onColor:(sheet:Sheet,color:string)=>Promise<void>
  onDelete:(sheet:Sheet)=>Promise<void>
}

export function SheetTabs({sheets,activeSheetId,saveState,saveLabel,onStatusClick,onSelect,onCreate,onRename,onDuplicate,onMove,onColor,onDelete}:Props){
  const scroller=useRef<HTMLDivElement>(null)
  const [menu,setMenu]=useState<string>(),[renaming,setRenaming]=useState<string>(),[name,setName]=useState(''),[pending,setPending]=useState(false),[error,setError]=useState('')
  const run=async(action:()=>Promise<void>)=>{setPending(true);setError('');try{await action();setMenu(undefined);setRenaming(undefined)}catch(reason){setError(reason instanceof Error?reason.message:'시트 작업을 완료하지 못했습니다.')}finally{setPending(false)}}
  const startRename=(sheet:Sheet)=>{setMenu(sheet.id);setRenaming(sheet.id);setName(sheet.name);setError('')}
  const submitRename=(sheet:Sheet)=>{const next=name.trim();if(!next||next===sheet.name){setRenaming(undefined);return}void run(()=>onRename(sheet,next))}
  const scroll=(direction:number)=>scroller.current?.scrollBy({left:direction*220,behavior:'smooth'})
  const menuSheet=sheets.find(sheet=>sheet.id===menu)
  return <div className="sheet-tabs">
    <button className="tab-nav" onClick={()=>scroll(-1)} aria-label="시트 탭 왼쪽으로"><ChevronLeft/></button>
    <button className="tab-nav" onClick={()=>scroll(1)} aria-label="시트 탭 오른쪽으로"><ChevronRight/></button>
    <button className="tab-add" disabled={pending} onClick={()=>run(onCreate)} aria-label="시트 추가"><Plus/></button>
    <div className="sheet-tab-scroller" ref={scroller}>
      {sheets.map(sheet=><div className="sheet-tab-wrap" key={sheet.id}>
        {renaming===sheet.id?<form className="sheet-tab-rename" onSubmit={event=>{event.preventDefault();submitRename(sheet)}}><input autoFocus value={name} maxLength={100} aria-label="시트 이름" onChange={event=>setName(event.target.value)} onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();setRenaming(undefined)}}}/><button disabled={pending} aria-label="시트 이름 저장"><Check/></button></form>:<button className={`sheet-tab-main ${sheet.id===activeSheetId?'active':''}`} onClick={()=>onSelect(sheet)} onDoubleClick={()=>startRename(sheet)}><i className={sheet.color?'':'empty'} style={sheet.color?{background:sheet.color}:undefined}/><span>{sheet.name}</span></button>}
        {sheet.id===activeSheetId&&renaming!==sheet.id&&<button className="sheet-tab-menu-trigger" aria-label={`${sheet.name} 시트 메뉴`} onClick={()=>setMenu(current=>current===sheet.id?undefined:sheet.id)}><MoreHorizontal/></button>}
      </div>)}
    </div>
    {menuSheet&&renaming!==menuSheet.id&&<div className="sheet-tab-menu" role="menu">
      <strong>{menuSheet.name}</strong>
      <button role="menuitem" onClick={()=>startRename(menuSheet)}><Save/> 이름 변경</button>
      <button role="menuitem" disabled={pending} onClick={()=>run(()=>onDuplicate(menuSheet))}><Copy/> 복제</button>
      <div className="sheet-move-actions"><button disabled={pending||menuSheet.position===0} onClick={()=>run(()=>onMove(menuSheet,menuSheet.position-1))}><ChevronLeft/> 왼쪽</button><button disabled={pending||menuSheet.position===sheets.length-1} onClick={()=>run(()=>onMove(menuSheet,menuSheet.position+1))}>오른쪽 <ChevronRight/></button></div>
      <div className="sheet-color-title"><Palette/> 탭 색상</div><div className="sheet-color-list">{colors.map((color,index)=><button key={color||'none'} className={menuSheet.color===color?'selected':''} aria-label={color?`시트 색상 ${color}`:'시트 색상 없음'} onClick={()=>run(()=>onColor(menuSheet,color))}><i className={index===0?'none':''} style={color?{background:color}:undefined}/></button>)}</div>
      <button role="menuitem" className="danger" disabled={pending||sheets.length===1} onClick={()=>{if(confirm(`'${menuSheet.name}' 시트와 모든 셀을 삭제할까요?`))void run(()=>onDelete(menuSheet))}}><Trash2/> 삭제</button>
      {error&&<small className="sheet-menu-error">{error}</small>}
    </div>}
    {error&&<span className="sheet-tabs-error">{error}</span>}
    <button className="sheet-status" disabled={!onStatusClick} onClick={onStatusClick}>{saveState==='offline'?<CloudOff/>:<Cloud/>} {saveLabel}</button>
  </div>
}
