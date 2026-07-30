import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, address, newIdempotencyKey } from '../lib/api'
import { collaborationClientId } from '../lib/client'
import { enqueue, flushOutbox } from '../lib/outbox'
import { presenceColor, useCollaborationStore } from '../state/collaboration'
import { cellKey, useEditorStore } from '../state/editor'
import type { Cell, MutationResult } from '../types'

const HEADER_WIDTH=46
const HEADER_HEIGHT=27
const TOTAL_ROWS=10_000
const TOTAL_COLUMNS=500

function columnName(column:number){let value=column,result='';while(value){value--;result=String.fromCharCode(65+value%26)+result;value=Math.floor(value/26)}return result}
function parsedValue(raw:string):unknown{if(raw==='')return undefined;if(raw.toLowerCase()==='true')return true;if(raw.toLowerCase()==='false')return false;if(Number.isFinite(Number(raw))&&raw.trim()!=='')return Number(raw);return raw}

export function CanvasGrid({sheetId,version,onVersion}:{sheetId:string;version:number;onVersion:(version:number)=>void}) {
  const viewport=useRef<HTMLDivElement>(null),canvas=useRef<HTMLCanvasElement>(null)
  const [scroll,setScroll]=useState({left:0,top:0}),[size,setSize]=useState({width:900,height:500}),[draft,setDraft]=useState('')
  const {activeRow,activeColumn,editing,zoom,cells,select,setEditing,replaceRange,putCells,putCell,setSaveState,recordOperation}=useEditorStore()
  const collaborators=useCollaborationStore(state=>state.users),sendCursor=useCollaborationStore(state=>state.sendCursor)
  const rowHeight=27*zoom,columnWidth=108*zoom
  const activeCell=cells.get(cellKey(activeRow,activeColumn))
  const activeText=activeCell?.formula || (activeCell?.value == null?'':String(activeCell.value))

  useEffect(()=>{setDraft(activeText)},[activeText,activeRow,activeColumn])
  useEffect(()=>{if(!viewport.current)return;const observer=new ResizeObserver(([entry])=>setSize({width:Math.floor(entry.contentRect.width),height:Math.floor(entry.contentRect.height)}));observer.observe(viewport.current);return()=>observer.disconnect()},[])

  const visibleRange=useMemo(()=>{const startRow=Math.max(1,Math.floor(scroll.top/rowHeight)+1),startColumn=Math.max(1,Math.floor(scroll.left/columnWidth)+1);return{startRow,startColumn,endRow:Math.min(TOTAL_ROWS,startRow+Math.ceil(size.height/rowHeight)+3),endColumn:Math.min(TOTAL_COLUMNS,startColumn+Math.ceil(size.width/columnWidth)+3)}},[scroll,rowHeight,columnWidth,size])
  useEffect(()=>{const controller=new AbortController();const range=`${address(visibleRange.startRow,visibleRange.startColumn)}:${address(visibleRange.endRow,visibleRange.endColumn)}`;api<{items:Cell[]}>(`/api/v1/sheets/${sheetId}/ranges/${range}`,{signal:controller.signal}).then(result=>replaceRange(result.items,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn)).catch(()=>{});return()=>controller.abort()},[sheetId,version,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn,replaceRange])

  useEffect(()=>{const element=canvas.current;if(!element)return;const ratio=window.devicePixelRatio||1;element.width=size.width*ratio;element.height=size.height*ratio;element.style.width=`${size.width}px`;element.style.height=`${size.height}px`;const context=element.getContext('2d');if(!context)return;context.scale(ratio,ratio);context.fillStyle='#fff';context.fillRect(0,0,size.width,size.height);context.font=`${12*zoom}px Inter, Pretendard, sans-serif`;context.textBaseline='middle';
    const firstRow=Math.floor(scroll.top/rowHeight)+1,firstColumn=Math.floor(scroll.left/columnWidth)+1;const offsetY=HEADER_HEIGHT-(scroll.top%rowHeight),offsetX=HEADER_WIDTH-(scroll.left%columnWidth)
    context.fillStyle='#f7f9fb';context.fillRect(0,0,size.width,HEADER_HEIGHT);context.fillRect(0,0,HEADER_WIDTH,size.height)
    context.strokeStyle='#e4e8ec';context.lineWidth=1
    for(let index=0,x=offsetX,column=firstColumn;x<size.width;x+=columnWidth,index++,column++){
      context.beginPath();context.moveTo(Math.round(x)+.5,0);context.lineTo(Math.round(x)+.5,size.height);context.stroke();context.fillStyle='#52606d';context.textAlign='center';context.fillText(columnName(column),x+columnWidth/2,HEADER_HEIGHT/2)
    }
    for(let y=offsetY,row=firstRow;y<size.height;y+=rowHeight,row++){
      context.beginPath();context.moveTo(0,Math.round(y)+.5);context.lineTo(size.width,Math.round(y)+.5);context.stroke();context.fillStyle='#73808c';context.textAlign='right';context.fillText(String(row),HEADER_WIDTH-8,y+rowHeight/2)
    }
    context.save();context.beginPath();context.rect(HEADER_WIDTH,HEADER_HEIGHT,size.width-HEADER_WIDTH,size.height-HEADER_HEIGHT);context.clip()
    cells.forEach(cell=>{if(cell.row<visibleRange.startRow||cell.row>visibleRange.endRow||cell.column<visibleRange.startColumn||cell.column>visibleRange.endColumn)return;const x=HEADER_WIDTH+(cell.column-1)*columnWidth-scroll.left,y=HEADER_HEIGHT+(cell.row-1)*rowHeight-scroll.top;const style=cell.style??{};if(typeof style.background==='string'){context.fillStyle=style.background;context.fillRect(x+1,y+1,columnWidth-2,rowHeight-2)}const formulaError=typeof cell.value==='string'&&cell.value.startsWith('#');context.fillStyle=formulaError?'#c2413b':typeof style.color==='string'?style.color:'#1c2733';context.font=`${style.bold||formulaError?'600 ':'400 '}${12*zoom}px Inter, Pretendard, sans-serif`;context.textAlign=typeof cell.value==='number'?'right':'left';const padding=context.textAlign==='right'?columnWidth-7:7;const value=cell.value;context.fillText(value==null?'':String(value),x+padding,y+rowHeight/2,columnWidth-12)})
    Object.values(collaborators).forEach(user=>{if(user.client_id===collaborationClientId()||user.cursor?.sheet_id!==sheetId)return;const cursor=user.cursor;const x=HEADER_WIDTH+(cursor.column-1)*columnWidth-scroll.left,y=HEADER_HEIGHT+(cursor.row-1)*rowHeight-scroll.top;if(x+columnWidth<HEADER_WIDTH||y+rowHeight<HEADER_HEIGHT||x>size.width||y>size.height)return;const color=presenceColor(user.client_id);context.strokeStyle=color;context.lineWidth=2;context.strokeRect(Math.round(x)+2,Math.round(y)+2,Math.round(columnWidth)-4,Math.round(rowHeight)-4);context.fillStyle=color;context.font=`600 ${9*zoom}px Inter, Pretendard, sans-serif`;context.textAlign='left';const label=user.actor_id||'사용자';const labelWidth=Math.min(columnWidth,context.measureText(label).width+10);context.fillRect(x+1,Math.max(HEADER_HEIGHT,y-15*zoom),labelWidth,14*zoom);context.fillStyle='#fff';context.fillText(label,x+5,Math.max(HEADER_HEIGHT+7*zoom,y-8*zoom),labelWidth-8)})
    const activeX=HEADER_WIDTH+(activeColumn-1)*columnWidth-scroll.left,activeY=HEADER_HEIGHT+(activeRow-1)*rowHeight-scroll.top;context.strokeStyle='#0f766e';context.lineWidth=2;context.strokeRect(Math.round(activeX)+1,Math.round(activeY)+1,Math.round(columnWidth)-2,Math.round(rowHeight)-2);context.fillStyle='#0f766e';context.fillRect(activeX+columnWidth-4,activeY+rowHeight-4,6,6);context.restore()
    context.fillStyle='#edf7f5';context.fillRect(0,0,HEADER_WIDTH,HEADER_HEIGHT);context.strokeStyle='#d9dfe5';context.strokeRect(.5,.5,HEADER_WIDTH-.5,HEADER_HEIGHT-.5)
  },[size,scroll,rowHeight,columnWidth,cells,activeRow,activeColumn,zoom,visibleRange,collaborators,sheetId])

  const handleApplied=useCallback((_operation:unknown,result:unknown)=>{const applied=result as MutationResult;onVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)recordOperation(applied.operation_id);setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)},[onVersion,recordOperation,setSaveState])

  const commit=useCallback(async(raw:string,row=activeRow,column=activeColumn)=>{
    const formula=raw.startsWith('=')?raw:''
    let value:unknown=formula?undefined:parsedValue(raw)
    if(formula&&navigator.onLine){
      const formulaCells:Record<string,unknown>={}
      cells.forEach(candidate=>{formulaCells[address(candidate.row,candidate.column)]=candidate.value})
      try{
        const evaluated=await api<{value?:unknown;error?:{code:string}}>(`/api/v1/formulas:evaluate`,{method:'POST',body:JSON.stringify({formula,cells:formulaCells})})
        value=evaluated.error?.code??evaluated.value
      }catch{value='#ERROR!'}
    }
    const cell:Cell={sheet_id:sheetId,row,column,value,formula,updated_at:new Date().toISOString()}
    putCell(cell);setEditing(false);setSaveState(navigator.onLine?'saving':'offline')
    const id=newIdempotencyKey()
    await enqueue({id,sheetId,attempts:0,createdAt:Date.now(),body:{base_version:version,idempotency_key:id,client_id:collaborationClientId(),cells:[{row,column,value,formula}]}})
    await flushOutbox(handleApplied)
  },[activeRow,activeColumn,sheetId,version,cells,putCell,setEditing,setSaveState,handleApplied])

  useEffect(()=>{const sync=()=>flushOutbox(handleApplied);window.addEventListener('online',sync);const timer=window.setInterval(sync,3000);sync();return()=>{window.removeEventListener('online',sync);window.clearInterval(timer)}},[handleApplied])

  const selectCell=(row:number,column:number)=>{select(row,column);sendCursor({sheet_id:sheetId,row,column})}
  const pointer=(event:React.MouseEvent)=>{const rect=canvas.current!.getBoundingClientRect();const x=event.clientX-rect.left,y=event.clientY-rect.top;if(x<HEADER_WIDTH||y<HEADER_HEIGHT)return;selectCell(Math.max(1,Math.floor((y-HEADER_HEIGHT+scroll.top)/rowHeight)+1),Math.max(1,Math.floor((x-HEADER_WIDTH+scroll.left)/columnWidth)+1));viewport.current?.focus()}
  const keyDown=(event:React.KeyboardEvent)=>{if(editing)return;if(event.key==='Enter'||event.key==='F2'){setEditing(true);event.preventDefault()}else if(event.key==='ArrowDown'){selectCell(activeRow+1,activeColumn);event.preventDefault()}else if(event.key==='ArrowUp'){selectCell(Math.max(1,activeRow-1),activeColumn);event.preventDefault()}else if(event.key==='ArrowRight'||event.key==='Tab'){selectCell(activeRow,activeColumn+1);event.preventDefault()}else if(event.key==='ArrowLeft'){selectCell(activeRow,Math.max(1,activeColumn-1));event.preventDefault()}else if(event.key==='Backspace'||event.key==='Delete'){commit('');event.preventDefault()}else if(event.key.length===1&&!event.metaKey&&!event.ctrlKey){setDraft(event.key);setEditing(true);event.preventDefault()}}
  const paste=(event:React.ClipboardEvent)=>{event.preventDefault();const worker=new Worker(new URL('../workers/paste.worker.ts',import.meta.url),{type:'module'});worker.onmessage=async(message:MessageEvent<Array<{row:number;column:number;value:unknown}>>)=>{const batch=message.data.slice(0,1000);putCells(batch.map(cell=>({sheet_id:sheetId,...cell,updated_at:new Date().toISOString()})));setSaveState(navigator.onLine?'saving':'offline');const id=newIdempotencyKey();await enqueue({id,sheetId,attempts:0,createdAt:Date.now(),body:{base_version:version,idempotency_key:id,client_id:collaborationClientId(),cells:batch}});await flushOutbox(handleApplied);worker.terminate()};worker.postMessage({text:event.clipboardData.getData('text/plain'),startRow:activeRow,startColumn:activeColumn})}
  const inputLeft=HEADER_WIDTH+(activeColumn-1)*columnWidth-scroll.left,inputTop=HEADER_HEIGHT+(activeRow-1)*rowHeight-scroll.top
  return <div className="grid-viewport" ref={viewport} tabIndex={0} onScroll={(event)=>setScroll({left:event.currentTarget.scrollLeft,top:event.currentTarget.scrollTop})} onKeyDown={keyDown} onPaste={paste} aria-label="스프레드시트 그리드">
    <div className="grid-spacer" style={{width:HEADER_WIDTH+TOTAL_COLUMNS*columnWidth,height:HEADER_HEIGHT+TOTAL_ROWS*rowHeight}}><canvas ref={canvas} className="grid-canvas" onClick={pointer} onDoubleClick={()=>setEditing(true)}/></div>
    {editing&&<input autoFocus className="cell-editor" style={{left:inputLeft,top:inputTop,width:columnWidth,height:rowHeight}} value={draft} onChange={(event)=>setDraft(event.target.value)} onBlur={()=>commit(draft)} onKeyDown={(event)=>{if(event.key==='Enter'){event.preventDefault();commit(draft)}else if(event.key==='Escape'){setEditing(false);setDraft(activeText)}}}/>}
    <div className="sr-only" aria-live="polite">선택한 셀 {address(activeRow,activeColumn)}, 값 {activeText||'비어 있음'}</div>
  </div>
}
