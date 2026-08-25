import { CornerDownRight,Plus,Table2 } from 'lucide-react'
import { useState } from 'react'
import { address } from '../lib/api'
import type { MergeRange } from '../lib/merge'
import type { Sheet,SheetTable } from '../types'
import './SheetTableDialog.css'
import { useDialog } from '../lib/useDialog'

type Draft={name:string;sheetId:string;range:string;headerRow:boolean}
const rangeText=(range:MergeRange)=>`${address(range.startRow,range.startColumn)}:${address(range.endRow,range.endColumn)}`
const cellPosition=(value:string):[number,number]|undefined=>{const match=/^\$?([A-Za-z]+)\$?([1-9]\d*)$/.exec(value.trim());if(!match)return;let column=0;for(const letter of match[1].toUpperCase())column=column*26+letter.charCodeAt(0)-64;const row=Number(match[2]);if(column>16384||row>1048576)return;return[row,column]}
const validTarget=(value:string)=>{const parts=value.split(':');if(parts.length!==2)return false;const start=cellPosition(parts[0]),end=cellPosition(parts[1]);return !!start&&!!end&&start[0]<=end[0]&&start[1]<=end[1]}
const validName=(value:string)=>{const name=value.trim();return /^[\p{L}_][\p{L}\p{N}_.]*$/u.test(name)&&!cellPosition(name)&&!/^(true|false)$/i.test(name)}
// 머리글 줄만 있으면 가리킬 자료가 없다. 저장할 때 막히므로 미리 알려 준다.
const hasDataRows=(value:string,headerRow:boolean)=>{if(!validTarget(value))return false;const [start,end]=value.split(':').map(cellPosition);return !headerRow||(!!start&&!!end&&end[0]>start[0])}

export function SheetTableDialog({selection,activeSheetId,sheets,tables,onClose,onCreate,onUpdate,onDelete,onNavigate}:{
  selection:MergeRange;activeSheetId:string;sheets:Sheet[];tables:SheetTable[];onClose:()=>void
  onCreate:(input:Record<string,unknown>)=>Promise<SheetTable>;onUpdate:(id:string,input:Record<string,unknown>)=>Promise<SheetTable>;onDelete:(item:SheetTable)=>Promise<void>;onNavigate:(item:Pick<SheetTable,'sheet_id'|'range'>)=>void
}){
  const initial=():Draft=>({name:'',sheetId:activeSheetId,range:rangeText(selection),headerRow:true})
  const [selectedId,setSelectedId]=useState<string>(),[draft,setDraft]=useState<Draft>(initial),[saving,setSaving]=useState(false)
  const selected=tables.find(item=>item.id===selectedId)
  const choose=(item?:SheetTable)=>{setSelectedId(item?.id);setDraft(item?{name:item.name,sheetId:item.sheet_id,range:item.range,headerRow:item.header_row}:initial())}
  const save=async()=>{
    if(!validName(draft.name))return alert('표 이름은 문자 또는 밑줄로 시작하고 문자, 숫자, 밑줄, 마침표만 사용할 수 있습니다.')
    if(!validTarget(draft.range))return alert('A1:B10 처럼 두 칸을 이은 범위를 입력하세요.')
    if(!hasDataRows(draft.range,draft.headerRow))return alert('머리글 줄만 있으면 가리킬 자료가 없습니다. 자료 줄을 한 줄 이상 포함하세요.')
    setSaving(true)
    try{
      const input={name:draft.name.trim(),sheet_id:draft.sheetId,range:draft.range.toUpperCase(),header_row:draft.headerRow}
      const saved=selected?await onUpdate(selected.id,{...input,expected_revision:selected.revision}):await onCreate(input)
      choose(saved)
    }catch(error){alert(error instanceof Error?error.message:'표를 저장하지 못했습니다.')}finally{setSaving(false)}
  }
  const remove=async(item:SheetTable)=>{if(!confirm(`${item.name} 표를 삭제할까요? 이 이름을 쓰는 수식은 #NAME? 이 됩니다.`))return;setSaving(true);try{await onDelete(item);choose()}catch(error){alert(error instanceof Error?error.message:'표를 삭제하지 못했습니다.')}finally{setSaving(false)}}
  const dialog=useDialog<HTMLElement>(onClose)
  const example=draft.name||'매출표'
  return <div className="modal-backdrop"><div className="modal sheet-table-modal" role="dialog" ref={dialog as React.RefObject<any>} aria-modal="true" aria-label="표">
    <header><div><Table2/><div><h2>표</h2><p>범위에 이름을 붙이면 열 이름으로 가리킬 수 있습니다. 행과 열이 움직여도 수식이 그대로 맞습니다.</p></div></div><button aria-label="표 닫기" onClick={onClose}>×</button></header>
    <div className="sheet-table-layout">
      <aside><button className={!selected?'active':''} onClick={()=>choose()}><Plus/> 새 표</button>{tables.map(item=><button key={item.id} className={selected?.id===item.id?'active':''} onClick={()=>choose(item)}><span>{item.name}</span><em>{sheets.find(sheet=>sheet.id===item.sheet_id)?.name??'삭제된 시트'}!{item.range} · r{item.revision}</em></button>)}</aside>
      <section>
        <label>표 이름<input aria-label="표 이름" value={draft.name} maxLength={255} onChange={event=>setDraft(current=>({...current,name:event.target.value}))} placeholder="매출표"/></label>
        <div className="sheet-table-target">
          <label>시트<select aria-label="표 시트" value={draft.sheetId} onChange={event=>setDraft(current=>({...current,sheetId:event.target.value}))}>{sheets.map(sheet=><option key={sheet.id} value={sheet.id}>{sheet.name}</option>)}</select></label>
          <label>범위<input aria-label="표 범위" value={draft.range} onChange={event=>setDraft(current=>({...current,range:event.target.value}))} placeholder="A1:B20"/></label>
        </div>
        <label className="sheet-table-header"><input type="checkbox" aria-label="첫 줄이 머리글" checked={draft.headerRow} onChange={event=>setDraft(current=>({...current,headerRow:event.target.checked}))}/> 첫 줄이 머리글 (그 글자가 열 이름이 됩니다)</label>
        <p className="sheet-table-preview">수식 예: <code>=SUM({example}[금액])</code> · 표 전체는 <code>{example}</code> · 머리글까지는 <code>{example}[#전체]</code></p>
        <div className="modal-actions sheet-table-actions">{selected&&<><button className="secondary" onClick={()=>onNavigate(selected)}><CornerDownRight/> 표로 이동</button><button className="danger" disabled={saving} onClick={()=>remove(selected)}>삭제</button></>}<span/><button className="secondary" onClick={onClose}>닫기</button><button className="primary" disabled={saving||!validName(draft.name)||!validTarget(draft.range)} onClick={save}>{saving?'저장 중…':'저장'}</button></div>
      </section>
    </div>
  </div></div>
}
