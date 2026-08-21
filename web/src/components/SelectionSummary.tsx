import { useState } from 'react'
import { ContextMenu, type MenuItem } from './ContextMenu'
import { DEFAULT_SUMMARY, formatStat, shouldSummarize, SUMMARY_LABELS, summarizeSelection, type SummaryKey } from '../lib/selectionSummary'
import { selectedBounds, useEditorStore } from '../state/editor'

const STORAGE_KEY='kanpic.selection-summary'

function storedChoice():SummaryKey[]{
  try{
    const raw=JSON.parse(localStorage.getItem(STORAGE_KEY)??'null')
    if(Array.isArray(raw)&&raw.every(item=>item in SUMMARY_LABELS)&&raw.length>0)return raw as SummaryKey[]
  }catch{/* a browser that blocks storage just gets the default */}
  return DEFAULT_SUMMARY
}

/**
 * The running total of whatever is selected, in the corner every spreadsheet
 * keeps it. Which statistics appear is a personal choice, so it is remembered
 * in the browser rather than in the workbook.
 */
export function SelectionSummary(){
  const [chosen,setChosen]=useState<SummaryKey[]>(storedChoice)
  const [menu,setMenu]=useState<{x:number;y:number}>()
  const cells=useEditorStore(state=>state.cells)
  // Selecting the four scalars keeps this component from re-rendering on every
  // unrelated store change, which a derived object would cause.
  const activeRow=useEditorStore(state=>state.activeRow)
  const activeColumn=useEditorStore(state=>state.activeColumn)
  const anchorRow=useEditorStore(state=>state.anchorRow)
  const anchorColumn=useEditorStore(state=>state.anchorColumn)
  const bounds=selectedBounds({activeRow,activeColumn,anchorRow,anchorColumn})
  const stats=summarizeSelection(cells,bounds.startRow,bounds.startColumn,bounds.endRow,bounds.endColumn)
  const cellCount=(bounds.endRow-bounds.startRow+1)*(bounds.endColumn-bounds.startColumn+1)
  if(!shouldSummarize(stats,cellCount))return null
  const toggle=(key:SummaryKey)=>{
    const next=chosen.includes(key)?chosen.filter(item=>item!==key):[...chosen,key]
    if(next.length===0)return
    setChosen(next)
    try{localStorage.setItem(STORAGE_KEY,JSON.stringify(next))}catch{/* nothing to do */}
  }
  const items:MenuItem[]=(Object.keys(SUMMARY_LABELS) as SummaryKey[]).map(key=>({
    kind:'item',label:SUMMARY_LABELS[key],checked:chosen.includes(key),onSelect:()=>toggle(key),
  }))
  return <>
    <button className="selection-summary" aria-label="선택 범위 요약" title="클릭해 표시할 항목을 고릅니다"
      onClick={event=>setMenu({x:event.clientX,y:event.clientY})}>
      {chosen.map(key=><span key={key}><small>{SUMMARY_LABELS[key]}</small> {formatStat(key,stats)}</span>)}
    </button>
    {menu&&<ContextMenu x={menu.x} y={menu.y} items={items} label="요약 항목" onClose={()=>setMenu(undefined)}/>}
  </>
}
