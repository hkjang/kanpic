import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, address, newIdempotencyKey } from '../lib/api'
import { clipboardText, KANPIC_CLIPBOARD_TYPE, materializeFill, MAX_GRID_COLUMNS, MAX_GRID_ROWS, MAX_PASTE_CELLS, type FillRange, type KanpicClipboard, type PastedCell } from '../lib/clipboard'
import { collaborationClientId } from '../lib/client'
import { cellMerge,selectedMergedBounds,stripMergeStyle,type MergeRange } from '../lib/merge'
import { enqueue, flushOutbox } from '../lib/outbox'
import { createRowVisibility } from '../lib/rowVisibility'
import { optionForValue,optionLabel,validateClientInputs,validateClientValue,validationForCell } from '../lib/validation'
import { presenceColor, useCollaborationStore } from '../state/collaboration'
import { cellKey, selectedBounds, useEditorStore } from '../state/editor'
import type { Cell, DataValidation, MutationResult } from '../types'

const HEADER_WIDTH=46
const HEADER_HEIGHT=27
const TOTAL_ROWS=MAX_GRID_ROWS
const TOTAL_COLUMNS=MAX_GRID_COLUMNS

function columnName(column:number){let value=column,result='';while(value){value--;result=String.fromCharCode(65+value%26)+result;value=Math.floor(value/26)}return result}
function parsedValue(raw:string):unknown{if(raw==='')return undefined;if(raw.toLowerCase()==='true')return true;if(raw.toLowerCase()==='false')return false;if(Number.isFinite(Number(raw))&&raw.trim()!=='')return Number(raw);return raw}

export function CanvasGrid({sheetId,version,onVersion,hiddenRows=[],validations=[]}:{sheetId:string;version:number;onVersion:(version:number)=>void;hiddenRows?:number[];validations?:DataValidation[]}) {
  const viewport=useRef<HTMLDivElement>(null),canvas=useRef<HTMLCanvasElement>(null),dragging=useRef(false),filling=useRef(false),fillPreviewRef=useRef<FillRange|undefined>(undefined)
  const [scroll,setScroll]=useState({left:0,top:0}),[size,setSize]=useState({width:900,height:500}),[draft,setDraft]=useState(''),[fillPreview,setFillPreview]=useState<FillRange>(),[refreshToken,setRefreshToken]=useState(0)
  const editor=useEditorStore()
  const {activeRow,activeColumn,anchorRow,anchorColumn,editing,zoom,cells,select,setEditing,replaceRange,putCells,putCell,setSaveState,recordOperation}=editor
  const selection=selectedMergedBounds(cells,selectedBounds(editor))
  const collaborators=useCollaborationStore(state=>state.users)
  const sendCursor=useCollaborationStore(state=>state.sendCursor),sendSelection=useCollaborationStore(state=>state.sendSelection)
  const rowHeight=27*zoom,columnWidth=108*zoom
  const hiddenRowsKey=hiddenRows.join(','),rowVisibility=useMemo(()=>createRowVisibility(hiddenRows,TOTAL_ROWS),[hiddenRowsKey])
  const activeCell=cells.get(cellKey(activeRow,activeColumn))
  const activeValidation=validationForCell(validations,activeRow,activeColumn)
  const activeText=activeCell?.formula || (activeCell?.value == null?'':String(activeCell.value))

  useEffect(()=>{setDraft(activeText)},[activeText,activeRow,activeColumn])
  useEffect(()=>{if(rowVisibility.isHidden(activeRow))select(rowVisibility.firstVisibleAtOrAfter(activeRow),activeColumn)},[rowVisibility,activeRow,activeColumn,select])
  useEffect(()=>{dragging.current=false;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined)},[sheetId])
  useEffect(()=>{const rejected=(event:Event)=>{const detail=(event as CustomEvent<{message?:string}>).detail;setSaveState('error');setRefreshToken(value=>value+1);alert(detail?.message??'서버가 변경을 거부했습니다. 최신 값을 다시 불러옵니다.')};window.addEventListener('kanpic:outbox-rejected',rejected);return()=>window.removeEventListener('kanpic:outbox-rejected',rejected)},[setSaveState])
  useEffect(()=>{if(!viewport.current)return;const observer=new ResizeObserver(([entry])=>setSize({width:Math.floor(entry.contentRect.width),height:Math.floor(entry.contentRect.height)}));observer.observe(viewport.current);return()=>observer.disconnect()},[])

  const visibleRange=useMemo(()=>{const startDisplay=Math.max(1,Math.floor(scroll.top/rowHeight)+1),endDisplay=Math.min(rowVisibility.visibleCount,startDisplay+Math.ceil(size.height/rowHeight)+3),startColumn=Math.max(1,Math.floor(scroll.left/columnWidth)+1);return{startRow:rowVisibility.displayToActual(startDisplay),startColumn,endRow:rowVisibility.displayToActual(endDisplay),endColumn:Math.min(TOTAL_COLUMNS,startColumn+Math.ceil(size.width/columnWidth)+3)}},[scroll,rowHeight,columnWidth,size,rowVisibility])
  useEffect(()=>{const controller=new AbortController();const range=`${address(visibleRange.startRow,visibleRange.startColumn)}:${address(visibleRange.endRow,visibleRange.endColumn)}`;api<{items:Cell[]}>(`/api/v1/sheets/${sheetId}/ranges/${range}`,{signal:controller.signal}).then(result=>replaceRange(result.items,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn)).catch(()=>{});return()=>controller.abort()},[sheetId,version,refreshToken,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn,replaceRange])

  useEffect(()=>{
    const element=canvas.current
    if(!element)return
    const ratio=window.devicePixelRatio||1
    element.width=size.width*ratio;element.height=size.height*ratio;element.style.width=`${size.width}px`;element.style.height=`${size.height}px`
    const context=element.getContext('2d');if(!context)return
    context.scale(ratio,ratio);context.fillStyle='#fff';context.fillRect(0,0,size.width,size.height);context.font=`${12*zoom}px Inter, Pretendard, sans-serif`;context.textBaseline='middle'
    const firstDisplayRow=Math.floor(scroll.top/rowHeight)+1,firstColumn=Math.floor(scroll.left/columnWidth)+1
    const offsetY=HEADER_HEIGHT-(scroll.top%rowHeight),offsetX=HEADER_WIDTH-(scroll.left%columnWidth)
    context.fillStyle='#f7f9fb';context.fillRect(0,0,size.width,HEADER_HEIGHT);context.fillRect(0,0,HEADER_WIDTH,size.height)
    context.strokeStyle='#e4e8ec';context.lineWidth=1
    for(let x=offsetX,column=firstColumn;x<size.width;x+=columnWidth,column++){
      context.beginPath();context.moveTo(Math.round(x)+.5,0);context.lineTo(Math.round(x)+.5,size.height);context.stroke();context.fillStyle='#52606d';context.textAlign='center';context.fillText(columnName(column),x+columnWidth/2,HEADER_HEIGHT/2)
    }
    for(let y=offsetY,displayRow=firstDisplayRow;y<size.height&&displayRow<=rowVisibility.visibleCount;y+=rowHeight,displayRow++){
      const row=rowVisibility.displayToActual(displayRow)
      context.beginPath();context.moveTo(0,Math.round(y)+.5);context.lineTo(size.width,Math.round(y)+.5);context.stroke();context.fillStyle='#73808c';context.textAlign='right';context.fillText(String(row),HEADER_WIDTH-8,y+rowHeight/2)
    }
    context.save();context.beginPath();context.rect(HEADER_WIDTH,HEADER_HEIGHT,size.width-HEADER_WIDTH,size.height-HEADER_HEIGHT);context.clip()
    const mergedRanges=new Map<string,{range:MergeRange;representative:Cell}>()
    const drawCell=(cell:Cell,x:number,y:number,width=columnWidth,height=rowHeight)=>{
      const style=cell.style??{},validation=validationForCell(validations,cell.row,cell.column),validationOption=validation?.rule_type==='list'?optionForValue(validation,cell.value):undefined
      context.fillStyle=typeof style.background==='string'?style.background:'#fff';context.fillRect(x+1,y+1,width-2,height-2)
      if(validation?.display_style==='chip'&&validationOption?.color){context.fillStyle=validationOption.color;context.beginPath();context.roundRect(x+4,y+4,width-8,height-8,6);context.fill()}
      const formulaError=typeof cell.value==='string'&&cell.value.startsWith('#')
      const fontSize=typeof style.font_size==='number'?style.font_size:12,fontFamily=typeof style.font_family==='string'?JSON.stringify(style.font_family):'Inter, Pretendard, sans-serif'
      context.fillStyle=formulaError?'#c2413b':typeof style.color==='string'?style.color:'#1c2733';context.font=`${style.italic===true?'italic ':''}${style.bold||formulaError?'600':'400'} ${fontSize*zoom}px ${fontFamily}`
      const alignment=validation?.display_style==='chip'?'left':style.horizontal_align==='left'||style.horizontal_align==='center'||style.horizontal_align==='right'?style.horizontal_align:typeof cell.value==='number'?'right':'left'
      context.textAlign=alignment
      const text=validationOption?optionLabel(validationOption):cell.value==null?'':String(cell.value),textX=alignment==='right'?x+width-7:alignment==='center'?x+width/2:x+(validation?.display_style==='chip'?10:7)
      const vertical=style.vertical_align==='top'||style.vertical_align==='bottom'||style.vertical_align==='middle'?style.vertical_align:'middle'
      const textY=vertical==='top'?y+Math.max(4,fontSize*zoom/2+3):vertical==='bottom'?y+height-Math.max(4,fontSize*zoom/2+3):y+height/2
      const rotation=typeof style.text_rotation==='number'?style.text_rotation:0,maxTextWidth=Math.max(0,width-12)
      context.save();context.translate(textX,textY);if(rotation)context.rotate(rotation*Math.PI/180);context.fillText(text,0,0,maxTextWidth)
      if(text&&(style.underline===true||style.strike===true)){const measured=Math.min(context.measureText(text).width,maxTextWidth),start=alignment==='right'?-measured:alignment==='center'?-measured/2:0;context.strokeStyle=context.fillStyle;context.lineWidth=Math.max(1,zoom);if(style.underline===true){context.beginPath();context.moveTo(start,fontSize*zoom*.48);context.lineTo(start+measured,fontSize*zoom*.48);context.stroke()}if(style.strike===true){context.beginPath();context.moveTo(start,0);context.lineTo(start+measured,0);context.stroke()}}
      context.restore()
      if(validation?.rule_type==='list'&&validation.show_dropdown&&validation.display_style!=='plain'){context.fillStyle='#52606d';context.beginPath();context.moveTo(x+width-13,y+height/2-2);context.lineTo(x+width-7,y+height/2-2);context.lineTo(x+width-10,y+height/2+2);context.closePath();context.fill()}
      if(validation){const checked=validateClientValue(validation,cell.value);if(!checked.valid&&!checked.deferred){context.fillStyle='#dc2626';context.beginPath();context.moveTo(x+width-9,y+1);context.lineTo(x+width-1,y+1);context.lineTo(x+width-1,y+9);context.closePath();context.fill()}}
    }
    cells.forEach(cell=>{
      if(rowVisibility.isHidden(cell.row)||cell.row<visibleRange.startRow||cell.row>visibleRange.endRow||cell.column<visibleRange.startColumn||cell.column>visibleRange.endColumn)return
      const merged=cellMerge(cell)
      if(merged){const key=`${merged.startRow}:${merged.startColumn}:${merged.endRow}:${merged.endColumn}`,current=mergedRanges.get(key);if(!current||cell.row===merged.startRow&&cell.column===merged.startColumn)mergedRanges.set(key,{range:merged,representative:cell});return}
      const x=HEADER_WIDTH+(cell.column-1)*columnWidth-scroll.left,y=HEADER_HEIGHT+(rowVisibility.actualToDisplay(cell.row)-1)*rowHeight-scroll.top
      drawCell(cell,x,y)
    })
    mergedRanges.forEach(({range,representative})=>{const visibleRows=rowVisibility.countVisible(range.startRow,range.endRow);if(visibleRows===0)return;const firstVisible=rowVisibility.firstVisibleAtOrAfter(range.startRow),x=HEADER_WIDTH+(range.startColumn-1)*columnWidth-scroll.left,y=HEADER_HEIGHT+(rowVisibility.actualToDisplay(firstVisible)-1)*rowHeight-scroll.top,width=(range.endColumn-range.startColumn+1)*columnWidth,height=visibleRows*rowHeight,anchor=cells.get(cellKey(range.startRow,range.startColumn)),display=anchor??{...representative,value:undefined,formula:undefined};drawCell(display,x,y,width,height);context.strokeStyle='#e4e8ec';context.lineWidth=1;context.strokeRect(Math.round(x)+.5,Math.round(y)+.5,Math.round(width),Math.round(height))})
    Object.values(collaborators).forEach(user=>{
      if(user.client_id===collaborationClientId()||user.selection?.sheet_id!==sheetId)return
      const remote=user.selection
      const startRow=Math.min(remote.start.row,remote.end.row),endRow=Math.max(remote.start.row,remote.end.row)
      const startColumn=Math.min(remote.start.column,remote.end.column),endColumn=Math.max(remote.start.column,remote.end.column)
      const visibleRows=rowVisibility.countVisible(startRow,endRow);if(visibleRows===0)return
      const visibleStart=rowVisibility.firstVisibleAtOrAfter(startRow),x=HEADER_WIDTH+(startColumn-1)*columnWidth-scroll.left,y=HEADER_HEIGHT+(rowVisibility.actualToDisplay(visibleStart)-1)*rowHeight-scroll.top
      const width=(endColumn-startColumn+1)*columnWidth,height=visibleRows*rowHeight,color=presenceColor(user.client_id)
      context.save();context.globalAlpha=.1;context.fillStyle=color;context.fillRect(x,y,width,height);context.restore();context.strokeStyle=color;context.lineWidth=2;context.strokeRect(Math.round(x)+1,Math.round(y)+1,Math.round(width)-2,Math.round(height)-2)
    })
    Object.values(collaborators).forEach(user=>{
      if(user.client_id===collaborationClientId()||user.cursor?.sheet_id!==sheetId)return
      const cursor=user.cursor;if(rowVisibility.isHidden(cursor.row))return
      const x=HEADER_WIDTH+(cursor.column-1)*columnWidth-scroll.left,y=HEADER_HEIGHT+(rowVisibility.actualToDisplay(cursor.row)-1)*rowHeight-scroll.top
      if(x+columnWidth<HEADER_WIDTH||y+rowHeight<HEADER_HEIGHT||x>size.width||y>size.height)return
      const color=presenceColor(user.client_id);context.strokeStyle=color;context.lineWidth=2;context.strokeRect(Math.round(x)+2,Math.round(y)+2,Math.round(columnWidth)-4,Math.round(rowHeight)-4);context.fillStyle=color;context.font=`600 ${9*zoom}px Inter, Pretendard, sans-serif`;context.textAlign='left'
      const label=user.actor_id||'사용자',labelWidth=Math.min(columnWidth,context.measureText(label).width+10)
      context.fillRect(x+1,Math.max(HEADER_HEIGHT,y-15*zoom),labelWidth,14*zoom);context.fillStyle='#fff';context.fillText(label,x+5,Math.max(HEADER_HEIGHT+7*zoom,y-8*zoom),labelWidth-8)
    })
    if(fillPreview){const fillVisibleStart=rowVisibility.firstVisibleAtOrAfter(fillPreview.startRow),fillX=HEADER_WIDTH+(fillPreview.startColumn-1)*columnWidth-scroll.left,fillY=HEADER_HEIGHT+(rowVisibility.actualToDisplay(fillVisibleStart)-1)*rowHeight-scroll.top,fillWidth=(fillPreview.endColumn-fillPreview.startColumn+1)*columnWidth,fillHeight=rowVisibility.countVisible(fillPreview.startRow,fillPreview.endRow)*rowHeight;context.fillStyle='rgba(15,118,110,.045)';context.fillRect(fillX,fillY,fillWidth,fillHeight);context.save();context.setLineDash([5,3]);context.strokeStyle='#0f766e';context.lineWidth=1;context.strokeRect(Math.round(fillX)+1,Math.round(fillY)+1,Math.round(fillWidth)-2,Math.round(fillHeight)-2);context.restore()}
    const selectionVisibleStart=rowVisibility.firstVisibleAtOrAfter(selection.startRow),selectionX=HEADER_WIDTH+(selection.startColumn-1)*columnWidth-scroll.left,selectionY=HEADER_HEIGHT+(rowVisibility.actualToDisplay(selectionVisibleStart)-1)*rowHeight-scroll.top
    const selectionWidth=(selection.endColumn-selection.startColumn+1)*columnWidth,selectionHeight=rowVisibility.countVisible(selection.startRow,selection.endRow)*rowHeight
    context.fillStyle='rgba(15,118,110,.08)';context.fillRect(selectionX,selectionY,selectionWidth,selectionHeight);context.strokeStyle='#0f766e';context.lineWidth=2;context.strokeRect(Math.round(selectionX)+1,Math.round(selectionY)+1,Math.round(selectionWidth)-2,Math.round(selectionHeight)-2)
    const activeMerge=cellMerge(activeCell),activeStartRow=activeMerge?.startRow??activeRow,activeStartColumn=activeMerge?.startColumn??activeColumn,activeEndRow=activeMerge?.endRow??activeRow,activeEndColumn=activeMerge?.endColumn??activeColumn
    const activeVisibleStart=rowVisibility.firstVisibleAtOrAfter(activeStartRow),activeX=HEADER_WIDTH+(activeStartColumn-1)*columnWidth-scroll.left,activeY=HEADER_HEIGHT+(rowVisibility.actualToDisplay(activeVisibleStart)-1)*rowHeight-scroll.top,activeWidth=(activeEndColumn-activeStartColumn+1)*columnWidth,activeHeight=rowVisibility.countVisible(activeStartRow,activeEndRow)*rowHeight
    context.strokeStyle='#0f766e';context.lineWidth=2;context.strokeRect(Math.round(activeX)+1,Math.round(activeY)+1,Math.round(activeWidth)-2,Math.round(activeHeight)-2);context.fillStyle='#0f766e';context.fillRect(selectionX+selectionWidth-4,selectionY+selectionHeight-4,6,6);context.restore()
    context.fillStyle='#edf7f5';context.fillRect(0,0,HEADER_WIDTH,HEADER_HEIGHT);context.strokeStyle='#d9dfe5';context.strokeRect(.5,.5,HEADER_WIDTH-.5,HEADER_HEIGHT-.5)
  },[size,scroll,rowHeight,columnWidth,cells,activeRow,activeColumn,activeCell,zoom,visibleRange,collaborators,sheetId,selection.startRow,selection.startColumn,selection.endRow,selection.endColumn,fillPreview,rowVisibility,validations])

  const handleApplied=useCallback((_operation:unknown,result:unknown)=>{const applied=result as MutationResult;onVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)recordOperation(applied.operation_id);setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)},[onVersion,recordOperation,setSaveState])

  const queueCells=useCallback(async(inputs:PastedCell[],endpoint:'batch'|'paste'|'fill')=>{
    if(inputs.length===0)return
    if(inputs.length>(endpoint==='batch'?1000:MAX_PASTE_CELLS)){setSaveState('error');alert(endpoint==='batch'?'한 번에 최대 1,000셀까지 변경할 수 있습니다.':`${endpoint==='fill'?'자동 채우기':'붙여넣기'}는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`);return}
    const checked=validateClientInputs(validations,inputs)
    if(checked.rejected.length){const first=checked.rejected[0];setSaveState('error');alert(`${address(first.row,first.column)}: ${first.message}`);return}
    if(checked.warnings.length&&!confirm(`${checked.warnings.length.toLocaleString()}개 셀이 데이터 검증 조건을 만족하지 않습니다. 그래도 입력할까요?`))return
    const updatedAt=new Date().toISOString()
    putCells(inputs.map(cell=>({sheet_id:sheetId,...cell,updated_at:updatedAt})))
    setSaveState(navigator.onLine?'saving':'offline')
    const id=newIdempotencyKey()
    await enqueue({id,sheetId,endpoint,attempts:0,createdAt:Date.now(),body:{base_version:version,idempotency_key:id,client_id:collaborationClientId(),cells:inputs}})
    await flushOutbox(handleApplied)
  },[handleApplied,putCells,setSaveState,sheetId,version,validations])

  const saveCell=useCallback(async(value:unknown,formula:string,row:number,column:number)=>{
    const style=cells.get(cellKey(row,column))?.style,input={row,column,value,formula,style}
    const checked=validateClientInputs(validations,[input])
    if(checked.rejected.length){setSaveState('error');alert(`${address(row,column)}: ${checked.rejected[0].message}`);return false}
    if(checked.warnings.length&&!confirm(`${address(row,column)} 값이 데이터 검증 조건을 만족하지 않습니다. 그래도 입력할까요?`))return false
    const cell:Cell={sheet_id:sheetId,...input,updated_at:new Date().toISOString()}
    putCell(cell);setEditing(false);setSaveState(navigator.onLine?'saving':'offline')
    const id=newIdempotencyKey()
    await enqueue({id,sheetId,endpoint:'batch',attempts:0,createdAt:Date.now(),body:{base_version:version,idempotency_key:id,client_id:collaborationClientId(),cells:[input]}})
    await flushOutbox(handleApplied);return true
  },[sheetId,version,cells,putCell,setEditing,setSaveState,handleApplied,validations])

  const commit=useCallback(async(raw:string,row=activeRow,column=activeColumn)=>{
    const formula=raw.startsWith('=')?raw:''
    let value:unknown=formula?undefined:parsedValue(raw)
    if(formula&&navigator.onLine){
      const formulaCells:Record<string,unknown>={}
      cells.forEach(candidate=>{formulaCells[address(candidate.row,candidate.column)]=candidate.value})
      try{const evaluated=await api<{value?:unknown;error?:{code:string}}>(`/api/v1/formulas:evaluate`,{method:'POST',body:JSON.stringify({formula,cells:formulaCells})});value=evaluated.error?.code??evaluated.value}catch{value='#ERROR!'}
    }
    await saveCell(value,formula,row,column)
  },[activeRow,activeColumn,cells,saveCell])

  useEffect(()=>{const sync=()=>flushOutbox(handleApplied);window.addEventListener('online',sync);const timer=window.setInterval(sync,3000);sync();return()=>{window.removeEventListener('online',sync);window.clearInterval(timer)}},[handleApplied])

  const selectionPayload=useCallback(()=>{
    const rows=selection.endRow-selection.startRow+1,columns=selection.endColumn-selection.startColumn+1
    if(rows*columns>MAX_PASTE_CELLS)throw new Error(`복사와 잘라내기는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`)
    const payload:KanpicClipboard={version:1,sourceRow:selection.startRow,sourceColumn:selection.startColumn,rows,columns,cells:[]}
    for(let row=selection.startRow;row<=selection.endRow;row+=1)for(let column=selection.startColumn;column<=selection.endColumn;column+=1){
      const cell=cells.get(cellKey(row,column))
      payload.cells.push({rowOffset:row-selection.startRow,columnOffset:column-selection.startColumn,value:cell?.value,formula:cell?.formula||undefined,style:stripMergeStyle(cell?.style)})
    }
    return payload
  },[cells,selection.endColumn,selection.endRow,selection.startColumn,selection.startRow])
  const fillSelection=useCallback(async(target:FillRange)=>{try{const inputs=materializeFill(selectionPayload(),target);await queueCells(inputs,'fill')}catch(error){setSaveState('error');alert(error instanceof Error?error.message:'자동 채우기를 적용하지 못했습니다.')}},[queueCells,selectionPayload,setSaveState])

  const selectCell=useCallback((row:number,column:number,extend=false)=>{
    let nextRow=Math.max(1,Math.min(TOTAL_ROWS,row)),nextColumn=Math.max(1,Math.min(TOTAL_COLUMNS,column));if(rowVisibility.isHidden(nextRow))nextRow=row>=activeRow?rowVisibility.firstVisibleAtOrAfter(nextRow):rowVisibility.lastVisibleAtOrBefore(nextRow)
    const merged=!extend?cellMerge(cells.get(cellKey(nextRow,nextColumn))):undefined
    if(merged){nextRow=merged.startRow;nextColumn=merged.startColumn}
    select(nextRow,nextColumn,extend);sendCursor({sheet_id:sheetId,row:nextRow,column:nextColumn})
    const start=extend?{row:Math.min(anchorRow,nextRow),column:Math.min(anchorColumn,nextColumn)}:{row:merged?.startRow??nextRow,column:merged?.startColumn??nextColumn}
    const end=extend?{row:Math.max(anchorRow,nextRow),column:Math.max(anchorColumn,nextColumn)}:{row:merged?.endRow??nextRow,column:merged?.endColumn??nextColumn}
    sendSelection({sheet_id:sheetId,start,end})
  },[anchorRow,anchorColumn,cells,select,sendCursor,sendSelection,sheetId,rowVisibility,activeRow])
  const pointerPosition=(event:React.PointerEvent<HTMLCanvasElement>)=>{const rect=canvas.current!.getBoundingClientRect();return{x:event.clientX-rect.left,y:event.clientY-rect.top}}
  const pointCell=(event:React.PointerEvent<HTMLCanvasElement>)=>{const{x,y}=pointerPosition(event);if(x<HEADER_WIDTH||y<HEADER_HEIGHT)return;const displayRow=Math.max(1,Math.min(rowVisibility.visibleCount,Math.floor((y-HEADER_HEIGHT+scroll.top)/rowHeight)+1));return{row:rowVisibility.displayToActual(displayRow),column:Math.max(1,Math.min(TOTAL_COLUMNS,Math.floor((x-HEADER_WIDTH+scroll.left)/columnWidth)+1))}}
  const onFillHandle=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(rowVisibility.hidden.length>0)return false;const{x,y}=pointerPosition(event),handleX=HEADER_WIDTH+selection.endColumn*columnWidth-scroll.left,handleY=HEADER_HEIGHT+selection.endRow*rowHeight-scroll.top;return Math.abs(x-handleX)<=8&&Math.abs(y-handleY)<=8}
  const pointerDown=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(event.button!==0)return;if(onFillHandle(event)){filling.current=true;fillPreviewRef.current={...selection};setFillPreview({...selection});event.currentTarget.style.cursor='crosshair';event.currentTarget.setPointerCapture(event.pointerId);viewport.current?.focus();event.preventDefault();return}const cell=pointCell(event);if(!cell)return;dragging.current=true;event.currentTarget.setPointerCapture(event.pointerId);selectCell(cell.row,cell.column,event.shiftKey);viewport.current?.focus()}
  const pointerMove=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(filling.current){const cell=pointCell(event);if(!cell)return;const next={startRow:Math.min(selection.startRow,cell.row),startColumn:Math.min(selection.startColumn,cell.column),endRow:Math.max(selection.endRow,cell.row),endColumn:Math.max(selection.endColumn,cell.column)};fillPreviewRef.current=next;setFillPreview(next);return}if(!dragging.current){event.currentTarget.style.cursor=onFillHandle(event)?'crosshair':'default';return}const cell=pointCell(event);if(cell)selectCell(cell.row,cell.column,true)}
  const pointerUp=(event:React.PointerEvent<HTMLCanvasElement>)=>{dragging.current=false;const target=fillPreviewRef.current,shouldFill=filling.current;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined);event.currentTarget.style.cursor='default';if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId);if(shouldFill&&target)void fillSelection(target)}
  const pointerCancel=(event:React.PointerEvent<HTMLCanvasElement>)=>{dragging.current=false;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined);event.currentTarget.style.cursor='default';if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId)}
  const keyDown=(event:React.KeyboardEvent)=>{if(editing)return;if(event.key==='Enter'||event.key==='F2'){setEditing(true);event.preventDefault()}else if(event.key==='ArrowDown'){selectCell(rowVisibility.nextVisible(activeRow,1),activeColumn,event.shiftKey);event.preventDefault()}else if(event.key==='ArrowUp'){selectCell(rowVisibility.nextVisible(activeRow,-1),activeColumn,event.shiftKey);event.preventDefault()}else if(event.key==='ArrowRight'||event.key==='Tab'){selectCell(activeRow,activeColumn+1,event.shiftKey);event.preventDefault()}else if(event.key==='ArrowLeft'){selectCell(activeRow,activeColumn-1,event.shiftKey);event.preventDefault()}else if(event.key==='Backspace'||event.key==='Delete'){const count=(selection.endRow-selection.startRow+1)*(selection.endColumn-selection.startColumn+1);if(count===1)commit('');else void clearSelection();event.preventDefault()}else if(event.key.length===1&&!event.metaKey&&!event.ctrlKey){setDraft(event.key);setEditing(true);event.preventDefault()}}
  const writeClipboard=(event:React.ClipboardEvent)=>{try{const payload=selectionPayload();event.preventDefault();event.clipboardData.setData('text/plain',clipboardText(payload));event.clipboardData.setData(KANPIC_CLIPBOARD_TYPE,JSON.stringify(payload));return true}catch(error){event.preventDefault();alert(error instanceof Error?error.message:'선택 범위를 복사하지 못했습니다.');return false}}
  const copy=(event:React.ClipboardEvent)=>{writeClipboard(event)}
  const clearSelection=useCallback(async()=>{
    const count=(selection.endRow-selection.startRow+1)*(selection.endColumn-selection.startColumn+1)
    if(count>MAX_PASTE_CELLS){alert(`잘라내기와 삭제는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`);return}
    const empty:PastedCell[]=[]
    for(let row=selection.startRow;row<=selection.endRow;row+=1)for(let column=selection.startColumn;column<=selection.endColumn;column+=1)empty.push({row,column})
    await queueCells(empty,'paste')
  },[queueCells,selection.endColumn,selection.endRow,selection.startColumn,selection.startRow])
  const cut=(event:React.ClipboardEvent)=>{if(writeClipboard(event))void clearSelection()}
  const paste=(event:React.ClipboardEvent)=>{
    event.preventDefault()
    const worker=new Worker(new URL('../workers/paste.worker.ts',import.meta.url),{type:'module'})
    worker.onmessage=async(message:MessageEvent<{cells?:PastedCell[];error?:string}>)=>{try{if(message.data.error){setSaveState('error');alert(message.data.error);return}await queueCells(message.data.cells??[],'paste')}finally{worker.terminate()}}
    worker.onerror=()=>{setSaveState('error');worker.terminate();alert('붙여넣기 데이터를 처리하지 못했습니다.')}
    worker.postMessage({text:event.clipboardData.getData('text/plain'),internal:event.clipboardData.getData(KANPIC_CLIPBOARD_TYPE),startRow:activeRow,startColumn:activeColumn})
  }
  const activeMerge=cellMerge(activeCell),inputStartRow=activeMerge?.startRow??activeRow,inputStartColumn=activeMerge?.startColumn??activeColumn,inputEndRow=activeMerge?.endRow??activeRow,inputEndColumn=activeMerge?.endColumn??activeColumn
  const inputVisibleStart=rowVisibility.firstVisibleAtOrAfter(inputStartRow),inputLeft=HEADER_WIDTH+(inputStartColumn-1)*columnWidth-scroll.left,inputTop=HEADER_HEIGHT+(rowVisibility.actualToDisplay(inputVisibleStart)-1)*rowHeight-scroll.top,inputWidth=(inputEndColumn-inputStartColumn+1)*columnWidth,inputHeight=rowVisibility.countVisible(inputStartRow,inputEndRow)*rowHeight
  const dropdown=activeValidation?.rule_type==='list'&&activeValidation.show_dropdown?activeValidation:undefined
  const selectionAddress=selection.startRow===selection.endRow&&selection.startColumn===selection.endColumn?address(activeRow,activeColumn):`${address(selection.startRow,selection.startColumn)}:${address(selection.endRow,selection.endColumn)}`
  return <div className="grid-viewport" ref={viewport} tabIndex={0} onScroll={(event)=>setScroll({left:event.currentTarget.scrollLeft,top:event.currentTarget.scrollTop})} onKeyDown={keyDown} onCopy={copy} onCut={cut} onPaste={paste} aria-label="스프레드시트 그리드">
    <div className="grid-spacer" style={{width:HEADER_WIDTH+TOTAL_COLUMNS*columnWidth,height:HEADER_HEIGHT+rowVisibility.visibleCount*rowHeight}}><canvas ref={canvas} className="grid-canvas" onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp} onPointerCancel={pointerCancel} onDoubleClick={()=>setEditing(true)}/></div>
    {dropdown&&!editing&&<button className="cell-dropdown-trigger" aria-label={`${selectionAddress} 드롭다운 열기`} title={dropdown.help_text||'드롭다운 선택'} style={{left:inputLeft+inputWidth-23,top:inputTop,width:22,height:inputHeight}} onClick={()=>setEditing(true)}>▾</button>}
    {editing&&dropdown?<div className="cell-dropdown" role="listbox" aria-label={`${selectionAddress} 드롭다운`} style={{left:inputLeft,top:inputTop+inputHeight,minWidth:Math.max(inputWidth,180)}}>{dropdown.options?.map((option,index)=><button role="option" aria-selected={optionForValue(dropdown,activeCell?.value)===option} aria-label={`드롭다운 값 ${optionLabel(option)}`} key={index} onClick={()=>void saveCell(option.value,'',activeRow,activeColumn)}><i style={{background:option.color||'#e5e7eb'}}/><span>{optionLabel(option)}</span></button>)}<button className="cell-dropdown-cancel" onClick={()=>setEditing(false)}>취소</button></div>:editing&&<input autoFocus className="cell-editor" style={{left:inputLeft,top:inputTop,width:inputWidth,height:inputHeight}} value={draft} onChange={(event)=>setDraft(event.target.value)} onBlur={()=>commit(draft)} onKeyDown={(event)=>{if(event.key==='Enter'){event.preventDefault();commit(draft)}else if(event.key==='Escape'){setEditing(false);setDraft(activeText)}}}/>}
    <div className="sr-only" aria-live="polite">선택 범위 {selectionAddress}, 활성 셀 값 {activeText||'비어 있음'}{fillPreview?`, 자동 채우기 미리보기 ${address(fillPreview.startRow,fillPreview.startColumn)}:${address(fillPreview.endRow,fillPreview.endColumn)}`:''}</div>
  </div>
}
