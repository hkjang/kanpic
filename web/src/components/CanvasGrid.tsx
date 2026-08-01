import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, address, newIdempotencyKey } from '../lib/api'
import { clipboardText, KANPIC_CLIPBOARD_TYPE, materializeFill, MAX_GRID_COLUMNS, MAX_GRID_ROWS, MAX_PASTE_CELLS, type FillRange, type KanpicClipboard, type PastedCell } from '../lib/clipboard'
import { collaborationClientId } from '../lib/client'
import { cellMerge,selectedMergedBounds,stripMergeStyle,type MergeRange } from '../lib/merge'
import { enqueue, flushOutbox } from '../lib/outbox'
import { axisIndexAtViewport,axisViewportPosition,createDimensionAxis,type DimensionAxis } from '../lib/dimensionAxis'
import { optionForValue,optionLabel,validateClientInputs,validateClientValue,validationForCell } from '../lib/validation'
import { presenceColor, useCollaborationStore } from '../state/collaboration'
import { cellKey, selectedBounds, useEditorStore } from '../state/editor'
import type { Cell, DataValidation, MutationResult, SheetLayout } from '../types'

const HEADER_WIDTH=46
const HEADER_HEIGHT=27
const TOTAL_ROWS=MAX_GRID_ROWS
const TOTAL_COLUMNS=MAX_GRID_COLUMNS

function columnName(column:number){let value=column,result='';while(value){value--;result=String.fromCharCode(65+value%26)+result;value=Math.floor(value/26)}return result}
function parsedValue(raw:string):unknown{if(raw==='')return undefined;if(raw.toLowerCase()==='true')return true;if(raw.toLowerCase()==='false')return false;if(Number.isFinite(Number(raw))&&raw.trim()!=='')return Number(raw);return raw}
function parsedAddress(value:string){const match=/^([A-Z]+)([1-9]\d*)$/.exec(value.toUpperCase());if(!match)return;let column=0;for(const character of match[1])column=column*26+character.charCodeAt(0)-64;return{row:Number(match[2]),column}}
function formulaPreview(value:unknown){if(!Array.isArray(value))return value;const first=value[0];return Array.isArray(first)?first[0]:first}
const DEFAULT_LAYOUT:SheetLayout={revision:1,frozen_rows:0,frozen_columns:0}
function indexesIn(axis:DimensionAxis,start:number,end:number){const result:number[]=[];if(end<start)return result;let index=axis.firstVisibleAtOrAfter(start);while(index<=end&&result.length<10000){result.push(index);const next=axis.nextVisible(index,1);if(next<=index)break;index=next}return result}

export function CanvasGrid({sheetId,layout=DEFAULT_LAYOUT,version,onVersion,hiddenRows=[],validations=[]}:{sheetId:string;layout?:SheetLayout;version:number;onVersion:(version:number)=>void;hiddenRows?:number[];validations?:DataValidation[]}) {
  const viewport=useRef<HTMLDivElement>(null),canvas=useRef<HTMLCanvasElement>(null),dragging=useRef(false),filling=useRef(false),fillPreviewRef=useRef<FillRange|undefined>(undefined)
  const [scroll,setScroll]=useState({left:0,top:0}),[size,setSize]=useState({width:900,height:500}),[draft,setDraft]=useState(''),[fillPreview,setFillPreview]=useState<FillRange>(),[refreshToken,setRefreshToken]=useState(0)
  const editor=useEditorStore()
  const {activeRow,activeColumn,anchorRow,anchorColumn,editing,zoom,cells,select,setEditing,replaceRange,putCells,putCell,setSaveState,recordOperation}=editor
  const selection=selectedMergedBounds(cells,selectedBounds(editor))
  const collaborators=useCollaborationStore(state=>state.users)
  const sendCursor=useCollaborationStore(state=>state.sendCursor),sendSelection=useCollaborationStore(state=>state.sendSelection)
  const hiddenRowsKey=hiddenRows.join(','),layoutKey=JSON.stringify(layout)
  const rowAxis=useMemo(()=>createDimensionAxis({total:TOTAL_ROWS,defaultSize:27,sizes:layout.row_heights,hiddenRanges:layout.hidden_rows,hiddenIndexes:hiddenRows,zoom}),[hiddenRowsKey,layoutKey,zoom])
  const columnAxis=useMemo(()=>createDimensionAxis({total:TOTAL_COLUMNS,defaultSize:108,sizes:layout.column_widths,hiddenRanges:layout.hidden_columns,zoom}),[layoutKey,zoom])
  const frozenRows=Math.min(layout.frozen_rows??0,TOTAL_ROWS),frozenColumns=Math.min(layout.frozen_columns??0,TOTAL_COLUMNS)
  const activeCell=cells.get(cellKey(activeRow,activeColumn))
  const activeValidation=validationForCell(validations,activeRow,activeColumn)
  const activeText=activeCell?.formula || (activeCell?.value == null?'':String(activeCell.value))

  useEffect(()=>{setDraft(activeText)},[activeText,activeRow,activeColumn])
  useEffect(()=>{if(rowAxis.isHidden(activeRow)||columnAxis.isHidden(activeColumn))select(rowAxis.firstVisibleAtOrAfter(activeRow),columnAxis.firstVisibleAtOrAfter(activeColumn))},[rowAxis,columnAxis,activeRow,activeColumn,select])
  useEffect(()=>{dragging.current=false;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined)},[sheetId])
  useEffect(()=>{const rejected=(event:Event)=>{const detail=(event as CustomEvent<{message?:string}>).detail;setSaveState('error');setRefreshToken(value=>value+1);alert(detail?.message??'서버가 변경을 거부했습니다. 최신 값을 다시 불러옵니다.')};window.addEventListener('kanpic:outbox-rejected',rejected);return()=>window.removeEventListener('kanpic:outbox-rejected',rejected)},[setSaveState])
  useEffect(()=>{if(!viewport.current)return;const observer=new ResizeObserver(([entry])=>setSize({width:Math.floor(entry.contentRect.width),height:Math.floor(entry.contentRect.height)}));observer.observe(viewport.current);return()=>observer.disconnect()},[])

  const visibleRange=useMemo(()=>{const frozenHeight=rowAxis.offsetOf(frozenRows+1),frozenWidth=columnAxis.offsetOf(frozenColumns+1),startRow=rowAxis.firstVisibleAtOrAfter(Math.max(frozenRows+1,rowAxis.indexAtOffset(scroll.top+frozenHeight))),startColumn=columnAxis.firstVisibleAtOrAfter(Math.max(frozenColumns+1,columnAxis.indexAtOffset(scroll.left+frozenWidth))),endRow=rowAxis.lastVisibleAtOrBefore(rowAxis.indexAtOffset(scroll.top+Math.max(frozenHeight,size.height-HEADER_HEIGHT)+100*zoom)),endColumn=columnAxis.lastVisibleAtOrBefore(columnAxis.indexAtOffset(scroll.left+Math.max(frozenWidth,size.width-HEADER_WIDTH)+250*zoom));return{startRow,startColumn,endRow:Math.max(startRow,endRow),endColumn:Math.max(startColumn,endColumn)}},[scroll,size,rowAxis,columnAxis,frozenRows,frozenColumns,zoom])
  useEffect(()=>{const controller=new AbortController(),ranges=[visibleRange];if(frozenRows>0)ranges.push({startRow:rowAxis.firstVisibleAtOrAfter(1),endRow:rowAxis.lastVisibleAtOrBefore(frozenRows),startColumn:visibleRange.startColumn,endColumn:visibleRange.endColumn});if(frozenColumns>0)ranges.push({startRow:visibleRange.startRow,endRow:visibleRange.endRow,startColumn:columnAxis.firstVisibleAtOrAfter(1),endColumn:columnAxis.lastVisibleAtOrBefore(frozenColumns)});if(frozenRows>0&&frozenColumns>0)ranges.push({startRow:rowAxis.firstVisibleAtOrAfter(1),endRow:rowAxis.lastVisibleAtOrBefore(frozenRows),startColumn:columnAxis.firstVisibleAtOrAfter(1),endColumn:columnAxis.lastVisibleAtOrBefore(frozenColumns)});for(const selected of ranges){if(selected.endRow<selected.startRow||selected.endColumn<selected.startColumn)continue;const range=`${address(selected.startRow,selected.startColumn)}:${address(selected.endRow,selected.endColumn)}`;api<{items:Cell[]}>(`/api/v1/sheets/${sheetId}/ranges/${range}`,{signal:controller.signal}).then(result=>replaceRange(result.items,selected.startRow,selected.startColumn,selected.endRow,selected.endColumn)).catch(()=>{})}return()=>controller.abort()},[sheetId,version,refreshToken,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn,frozenRows,frozenColumns,rowAxis,columnAxis,replaceRange])

  useEffect(()=>{
    const element=canvas.current
    if(!element)return
    const ratio=window.devicePixelRatio||1
    element.width=size.width*ratio;element.height=size.height*ratio;element.style.width=`${size.width}px`;element.style.height=`${size.height}px`
    const context=element.getContext('2d');if(!context)return
    context.scale(ratio,ratio);context.fillStyle='#fff';context.fillRect(0,0,size.width,size.height);context.font=`${12*zoom}px Inter, Pretendard, sans-serif`;context.textBaseline='middle'
    const rowPosition=(row:number)=>HEADER_HEIGHT+axisViewportPosition(rowAxis,row,scroll.top,frozenRows),columnPosition=(column:number)=>HEADER_WIDTH+axisViewportPosition(columnAxis,column,scroll.left,frozenColumns)
    const rowVisible=(row:number)=>!rowAxis.isHidden(row)&&(row<=frozenRows||row>=visibleRange.startRow&&row<=visibleRange.endRow),columnVisible=(column:number)=>!columnAxis.isHidden(column)&&(column<=frozenColumns||column>=visibleRange.startColumn&&column<=visibleRange.endColumn)
    const geometry=(startRow:number,startColumn:number,endRow:number,endColumn:number)=>{if(rowAxis.countVisible(startRow,endRow)===0||columnAxis.countVisible(startColumn,endColumn)===0)return;const firstRow=rowAxis.firstVisibleAtOrAfter(startRow),lastRow=rowAxis.lastVisibleAtOrBefore(endRow),firstColumn=columnAxis.firstVisibleAtOrAfter(startColumn),lastColumn=columnAxis.lastVisibleAtOrBefore(endColumn),x=columnPosition(firstColumn),y=rowPosition(firstRow);return{x,y,width:columnPosition(lastColumn)+columnAxis.sizeOf(lastColumn)-x,height:rowPosition(lastRow)+rowAxis.sizeOf(lastRow)-y}}
    const mainRows=indexesIn(rowAxis,visibleRange.startRow,visibleRange.endRow),mainColumns=indexesIn(columnAxis,visibleRange.startColumn,visibleRange.endColumn),frozenRowIndexes=indexesIn(rowAxis,1,frozenRows),frozenColumnIndexes=indexesIn(columnAxis,1,frozenColumns)
    const rows=[...mainRows,...frozenRowIndexes],columns=[...mainColumns,...frozenColumnIndexes]
    context.fillStyle='#f7f9fb';context.fillRect(0,0,size.width,HEADER_HEIGHT);context.fillRect(0,0,HEADER_WIDTH,size.height)
    context.strokeStyle='#e4e8ec';context.lineWidth=1
    for(const column of columns){const x=columnPosition(column),width=columnAxis.sizeOf(column);if(x+width<HEADER_WIDTH||x>size.width)continue;context.fillStyle='#f7f9fb';context.fillRect(x,0,width,HEADER_HEIGHT);context.beginPath();context.moveTo(Math.round(x)+.5,0);context.lineTo(Math.round(x)+.5,size.height);context.stroke();context.fillStyle='#52606d';context.textAlign='center';context.fillText(columnName(column),x+width/2,HEADER_HEIGHT/2)}
    for(const row of rows){const y=rowPosition(row),height=rowAxis.sizeOf(row);if(y+height<HEADER_HEIGHT||y>size.height)continue;context.fillStyle='#f7f9fb';context.fillRect(0,y,HEADER_WIDTH,height);context.beginPath();context.moveTo(0,Math.round(y)+.5);context.lineTo(size.width,Math.round(y)+.5);context.stroke();context.fillStyle='#73808c';context.textAlign='right';context.fillText(String(row),HEADER_WIDTH-8,y+height/2)}
    context.save();context.beginPath();context.rect(HEADER_WIDTH,HEADER_HEIGHT,size.width-HEADER_WIDTH,size.height-HEADER_HEIGHT);context.clip()
    const mergedRanges=new Map<string,{range:MergeRange;representative:Cell}>()
    const drawCell=(cell:Cell,x:number,y:number,width:number,height:number)=>{
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
      if(cell.spill_source){context.save();context.setLineDash([2,2]);context.strokeStyle='#38a3a5';context.lineWidth=1;context.strokeRect(Math.round(x)+2,Math.round(y)+2,Math.round(width)-4,Math.round(height)-4);context.restore()}
    }
    const visibleCells=Array.from(cells.values()).filter(cell=>rowVisible(cell.row)&&columnVisible(cell.column)).sort((a,b)=>Number(a.row<=frozenRows)+Number(a.column<=frozenColumns)-Number(b.row<=frozenRows)-Number(b.column<=frozenColumns))
    visibleCells.forEach(cell=>{
      const merged=cellMerge(cell)
      if(merged){const key=`${merged.startRow}:${merged.startColumn}:${merged.endRow}:${merged.endColumn}`,current=mergedRanges.get(key);if(!current||cell.row===merged.startRow&&cell.column===merged.startColumn)mergedRanges.set(key,{range:merged,representative:cell});return}
      const x=columnPosition(cell.column),y=rowPosition(cell.row)
      drawCell(cell,x,y,columnAxis.sizeOf(cell.column),rowAxis.sizeOf(cell.row))
    })
    mergedRanges.forEach(({range,representative})=>{const box=geometry(range.startRow,range.startColumn,range.endRow,range.endColumn);if(!box)return;const anchor=cells.get(cellKey(range.startRow,range.startColumn)),display=anchor??{...representative,value:undefined,formula:undefined};drawCell(display,box.x,box.y,box.width,box.height);context.strokeStyle='#e4e8ec';context.lineWidth=1;context.strokeRect(Math.round(box.x)+.5,Math.round(box.y)+.5,Math.round(box.width),Math.round(box.height))})
    Object.values(collaborators).forEach(user=>{
      if(user.client_id===collaborationClientId()||user.selection?.sheet_id!==sheetId)return
      const remote=user.selection
      const startRow=Math.min(remote.start.row,remote.end.row),endRow=Math.max(remote.start.row,remote.end.row)
      const startColumn=Math.min(remote.start.column,remote.end.column),endColumn=Math.max(remote.start.column,remote.end.column)
      const box=geometry(startRow,startColumn,endRow,endColumn);if(!box)return
      const color=presenceColor(user.client_id)
      context.save();context.globalAlpha=.1;context.fillStyle=color;context.fillRect(box.x,box.y,box.width,box.height);context.restore();context.strokeStyle=color;context.lineWidth=2;context.strokeRect(Math.round(box.x)+1,Math.round(box.y)+1,Math.round(box.width)-2,Math.round(box.height)-2)
    })
    Object.values(collaborators).forEach(user=>{
      if(user.client_id===collaborationClientId()||user.cursor?.sheet_id!==sheetId)return
      const cursor=user.cursor;if(rowAxis.isHidden(cursor.row)||columnAxis.isHidden(cursor.column))return
      const x=columnPosition(cursor.column),y=rowPosition(cursor.row),cellWidth=columnAxis.sizeOf(cursor.column),cellHeight=rowAxis.sizeOf(cursor.row)
      if(x+cellWidth<HEADER_WIDTH||y+cellHeight<HEADER_HEIGHT||x>size.width||y>size.height)return
      const color=presenceColor(user.client_id);context.strokeStyle=color;context.lineWidth=2;context.strokeRect(Math.round(x)+2,Math.round(y)+2,Math.round(cellWidth)-4,Math.round(cellHeight)-4);context.fillStyle=color;context.font=`600 ${9*zoom}px Inter, Pretendard, sans-serif`;context.textAlign='left'
      const label=user.actor_id||'사용자',labelWidth=Math.min(cellWidth,context.measureText(label).width+10)
      context.fillRect(x+1,Math.max(HEADER_HEIGHT,y-15*zoom),labelWidth,14*zoom);context.fillStyle='#fff';context.fillText(label,x+5,Math.max(HEADER_HEIGHT+7*zoom,y-8*zoom),labelWidth-8)
    })
    if(fillPreview){const box=geometry(fillPreview.startRow,fillPreview.startColumn,fillPreview.endRow,fillPreview.endColumn);if(box){context.fillStyle='rgba(15,118,110,.045)';context.fillRect(box.x,box.y,box.width,box.height);context.save();context.setLineDash([5,3]);context.strokeStyle='#0f766e';context.lineWidth=1;context.strokeRect(Math.round(box.x)+1,Math.round(box.y)+1,Math.round(box.width)-2,Math.round(box.height)-2);context.restore()}}
    const selectionBox=geometry(selection.startRow,selection.startColumn,selection.endRow,selection.endColumn)
    if(selectionBox){context.fillStyle='rgba(15,118,110,.08)';context.fillRect(selectionBox.x,selectionBox.y,selectionBox.width,selectionBox.height);context.strokeStyle='#0f766e';context.lineWidth=2;context.strokeRect(Math.round(selectionBox.x)+1,Math.round(selectionBox.y)+1,Math.round(selectionBox.width)-2,Math.round(selectionBox.height)-2)}
    const activeMerge=cellMerge(activeCell),activeStartRow=activeMerge?.startRow??activeRow,activeStartColumn=activeMerge?.startColumn??activeColumn,activeEndRow=activeMerge?.endRow??activeRow,activeEndColumn=activeMerge?.endColumn??activeColumn
    const activeBox=geometry(activeStartRow,activeStartColumn,activeEndRow,activeEndColumn)
    if(activeBox){context.strokeStyle='#0f766e';context.lineWidth=2;context.strokeRect(Math.round(activeBox.x)+1,Math.round(activeBox.y)+1,Math.round(activeBox.width)-2,Math.round(activeBox.height)-2)}if(selectionBox){context.fillStyle='#0f766e';context.fillRect(selectionBox.x+selectionBox.width-4,selectionBox.y+selectionBox.height-4,6,6)}context.restore()
    const frozenHeight=rowAxis.offsetOf(frozenRows+1),frozenWidth=columnAxis.offsetOf(frozenColumns+1);context.strokeStyle='#98a9ad';context.lineWidth=2;if(frozenRows>0){context.beginPath();context.moveTo(0,HEADER_HEIGHT+frozenHeight+.5);context.lineTo(size.width,HEADER_HEIGHT+frozenHeight+.5);context.stroke()}if(frozenColumns>0){context.beginPath();context.moveTo(HEADER_WIDTH+frozenWidth+.5,0);context.lineTo(HEADER_WIDTH+frozenWidth+.5,size.height);context.stroke()}
    context.fillStyle='#edf7f5';context.fillRect(0,0,HEADER_WIDTH,HEADER_HEIGHT);context.strokeStyle='#d9dfe5';context.strokeRect(.5,.5,HEADER_WIDTH-.5,HEADER_HEIGHT-.5)
  },[size,scroll,rowAxis,columnAxis,frozenRows,frozenColumns,cells,activeRow,activeColumn,activeCell,zoom,visibleRange,collaborators,sheetId,selection.startRow,selection.startColumn,selection.endRow,selection.endColumn,fillPreview,validations])

  const handleApplied=useCallback((_operation:unknown,result:unknown)=>{const applied=result as MutationResult;onVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)recordOperation(applied.operation_id);setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)},[onVersion,recordOperation,setSaveState])

  const queueCells=useCallback(async(inputs:PastedCell[],endpoint:'batch'|'paste'|'fill')=>{
    if(inputs.length===0)return
    if(inputs.length>(endpoint==='batch'?1000:MAX_PASTE_CELLS)){setSaveState('error');alert(endpoint==='batch'?'한 번에 최대 1,000셀까지 변경할 수 있습니다.':`${endpoint==='fill'?'자동 채우기':'붙여넣기'}는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`);return}
    const spillChild=inputs.find(input=>cells.get(cellKey(input.row,input.column))?.spill_source)
    if(spillChild){const source=cells.get(cellKey(spillChild.row,spillChild.column))?.spill_source;setSaveState('error');alert(`${address(spillChild.row,spillChild.column)}은(는) ${source} 배열 수식의 결과입니다. 원본 수식 셀을 편집하세요.`);return}
    const checked=validateClientInputs(validations,inputs)
    if(checked.rejected.length){const first=checked.rejected[0];setSaveState('error');alert(`${address(first.row,first.column)}: ${first.message}`);return}
    if(checked.warnings.length&&!confirm(`${checked.warnings.length.toLocaleString()}개 셀이 데이터 검증 조건을 만족하지 않습니다. 그래도 입력할까요?`))return
    const updatedAt=new Date().toISOString()
    putCells(inputs.map(cell=>({sheet_id:sheetId,...cell,updated_at:updatedAt})))
    setSaveState(navigator.onLine?'saving':'offline')
    const id=newIdempotencyKey()
    await enqueue({id,sheetId,endpoint,attempts:0,createdAt:Date.now(),body:{base_version:version,idempotency_key:id,client_id:collaborationClientId(),cells:inputs}})
    await flushOutbox(handleApplied)
  },[cells,handleApplied,putCells,setSaveState,sheetId,version,validations])

  const saveCell=useCallback(async(value:unknown,formula:string,row:number,column:number)=>{
    const current=cells.get(cellKey(row,column))
    if(current?.spill_source){setSaveState('error');alert(`${address(row,column)}은(는) ${current.spill_source} 배열 수식의 결과입니다. 원본 수식 셀을 편집하세요.`);return false}
    const style=current?.style,input={row,column,value,formula,style}
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
      try{const evaluated=await api<{value?:unknown;error?:{code:string}}>(`/api/v1/formulas:evaluate`,{method:'POST',body:JSON.stringify({formula,cells:formulaCells})});value=evaluated.error?.code??formulaPreview(evaluated.value)}catch{value='#ERROR!'}
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
    let nextRow=Math.max(1,Math.min(TOTAL_ROWS,row)),nextColumn=Math.max(1,Math.min(TOTAL_COLUMNS,column));if(rowAxis.isHidden(nextRow))nextRow=row>=activeRow?rowAxis.firstVisibleAtOrAfter(nextRow):rowAxis.lastVisibleAtOrBefore(nextRow);if(columnAxis.isHidden(nextColumn))nextColumn=column>=activeColumn?columnAxis.firstVisibleAtOrAfter(nextColumn):columnAxis.lastVisibleAtOrBefore(nextColumn)
    const merged=!extend?cellMerge(cells.get(cellKey(nextRow,nextColumn))):undefined
    if(merged){nextRow=merged.startRow;nextColumn=merged.startColumn}
    select(nextRow,nextColumn,extend);sendCursor({sheet_id:sheetId,row:nextRow,column:nextColumn})
    const start=extend?{row:Math.min(anchorRow,nextRow),column:Math.min(anchorColumn,nextColumn)}:{row:merged?.startRow??nextRow,column:merged?.startColumn??nextColumn}
    const end=extend?{row:Math.max(anchorRow,nextRow),column:Math.max(anchorColumn,nextColumn)}:{row:merged?.endRow??nextRow,column:merged?.endColumn??nextColumn}
    sendSelection({sheet_id:sheetId,start,end})
  },[anchorRow,anchorColumn,cells,select,sendCursor,sendSelection,sheetId,rowAxis,columnAxis,activeRow,activeColumn])
  const pointerPosition=(event:React.PointerEvent<HTMLCanvasElement>)=>{const rect=canvas.current!.getBoundingClientRect();return{x:event.clientX-rect.left,y:event.clientY-rect.top}}
  const pointCell=(event:React.PointerEvent<HTMLCanvasElement>)=>{const{x,y}=pointerPosition(event);if(x<HEADER_WIDTH||y<HEADER_HEIGHT)return;return{row:axisIndexAtViewport(rowAxis,y-HEADER_HEIGHT,scroll.top,frozenRows),column:axisIndexAtViewport(columnAxis,x-HEADER_WIDTH,scroll.left,frozenColumns)}}
  const onFillHandle=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(rowAxis.hidden.length>0||columnAxis.hidden.length>0)return false;const{x,y}=pointerPosition(event),handleX=HEADER_WIDTH+axisViewportPosition(columnAxis,selection.endColumn,scroll.left,frozenColumns)+columnAxis.sizeOf(selection.endColumn),handleY=HEADER_HEIGHT+axisViewportPosition(rowAxis,selection.endRow,scroll.top,frozenRows)+rowAxis.sizeOf(selection.endRow);return Math.abs(x-handleX)<=8&&Math.abs(y-handleY)<=8}
  const pointerDown=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(event.button!==0)return;if(onFillHandle(event)){filling.current=true;fillPreviewRef.current={...selection};setFillPreview({...selection});event.currentTarget.style.cursor='crosshair';event.currentTarget.setPointerCapture(event.pointerId);viewport.current?.focus();event.preventDefault();return}const cell=pointCell(event);if(!cell)return;dragging.current=true;event.currentTarget.setPointerCapture(event.pointerId);selectCell(cell.row,cell.column,event.shiftKey);viewport.current?.focus()}
  const pointerMove=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(filling.current){const cell=pointCell(event);if(!cell)return;const next={startRow:Math.min(selection.startRow,cell.row),startColumn:Math.min(selection.startColumn,cell.column),endRow:Math.max(selection.endRow,cell.row),endColumn:Math.max(selection.endColumn,cell.column)};fillPreviewRef.current=next;setFillPreview(next);return}if(!dragging.current){event.currentTarget.style.cursor=onFillHandle(event)?'crosshair':'default';return}const cell=pointCell(event);if(cell)selectCell(cell.row,cell.column,true)}
  const pointerUp=(event:React.PointerEvent<HTMLCanvasElement>)=>{dragging.current=false;const target=fillPreviewRef.current,shouldFill=filling.current;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined);event.currentTarget.style.cursor='default';if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId);if(shouldFill&&target)void fillSelection(target)}
  const pointerCancel=(event:React.PointerEvent<HTMLCanvasElement>)=>{dragging.current=false;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined);event.currentTarget.style.cursor='default';if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId)}
  const editActiveCell=useCallback(()=>{if(activeCell?.spill_source){const source=parsedAddress(activeCell.spill_source);if(source){selectCell(source.row,source.column);setEditing(true);return}}setEditing(true)},[activeCell,selectCell,setEditing])
  const keyDown=(event:React.KeyboardEvent)=>{if(editing)return;if(event.key==='Enter'||event.key==='F2'){editActiveCell();event.preventDefault()}else if(event.key==='ArrowDown'){selectCell(rowAxis.nextVisible(activeRow,1),activeColumn,event.shiftKey);event.preventDefault()}else if(event.key==='ArrowUp'){selectCell(rowAxis.nextVisible(activeRow,-1),activeColumn,event.shiftKey);event.preventDefault()}else if(event.key==='ArrowRight'||event.key==='Tab'){selectCell(activeRow,columnAxis.nextVisible(activeColumn,1),event.shiftKey);event.preventDefault()}else if(event.key==='ArrowLeft'){selectCell(activeRow,columnAxis.nextVisible(activeColumn,-1),event.shiftKey);event.preventDefault()}else if(event.key==='Backspace'||event.key==='Delete'){const count=(selection.endRow-selection.startRow+1)*(selection.endColumn-selection.startColumn+1);if(count===1)commit('');else void clearSelection();event.preventDefault()}else if(event.key.length===1&&!event.metaKey&&!event.ctrlKey){if(activeCell?.spill_source){const source=parsedAddress(activeCell.spill_source);if(source)selectCell(source.row,source.column);alert(`${address(activeRow,activeColumn)}은(는) ${activeCell.spill_source} 배열 수식의 결과입니다. 원본 수식 셀에서 입력하세요.`)}else{setDraft(event.key);setEditing(true)}event.preventDefault()}}
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
  const inputVisibleStart=rowAxis.firstVisibleAtOrAfter(inputStartRow),inputVisibleColumn=columnAxis.firstVisibleAtOrAfter(inputStartColumn),inputLeft=HEADER_WIDTH+axisViewportPosition(columnAxis,inputVisibleColumn,scroll.left,frozenColumns),inputTop=HEADER_HEIGHT+axisViewportPosition(rowAxis,inputVisibleStart,scroll.top,frozenRows),inputWidth=columnAxis.rangeSize(inputStartColumn,inputEndColumn),inputHeight=rowAxis.rangeSize(inputStartRow,inputEndRow)
  const dropdown=!activeCell?.spill_source&&activeValidation?.rule_type==='list'&&activeValidation.show_dropdown?activeValidation:undefined
  const selectionAddress=selection.startRow===selection.endRow&&selection.startColumn===selection.endColumn?address(activeRow,activeColumn):`${address(selection.startRow,selection.startColumn)}:${address(selection.endRow,selection.endColumn)}`
  return <div className="grid-viewport" ref={viewport} tabIndex={0} onScroll={(event)=>setScroll({left:event.currentTarget.scrollLeft,top:event.currentTarget.scrollTop})} onKeyDown={keyDown} onCopy={copy} onCut={cut} onPaste={paste} aria-label="스프레드시트 그리드">
    <div className="grid-spacer" style={{width:HEADER_WIDTH+columnAxis.extent,height:HEADER_HEIGHT+rowAxis.extent}}><canvas ref={canvas} className="grid-canvas" onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp} onPointerCancel={pointerCancel} onDoubleClick={editActiveCell}/></div>
    {dropdown&&!editing&&<button className="cell-dropdown-trigger" aria-label={`${selectionAddress} 드롭다운 열기`} title={dropdown.help_text||'드롭다운 선택'} style={{left:inputLeft+inputWidth-23,top:inputTop,width:22,height:inputHeight}} onClick={()=>setEditing(true)}>▾</button>}
    {editing&&dropdown?<div className="cell-dropdown" role="listbox" aria-label={`${selectionAddress} 드롭다운`} style={{left:inputLeft,top:inputTop+inputHeight,minWidth:Math.max(inputWidth,180)}}>{dropdown.options?.map((option,index)=><button role="option" aria-selected={optionForValue(dropdown,activeCell?.value)===option} aria-label={`드롭다운 값 ${optionLabel(option)}`} key={index} onClick={()=>void saveCell(option.value,'',activeRow,activeColumn)}><i style={{background:option.color||'#e5e7eb'}}/><span>{optionLabel(option)}</span></button>)}<button className="cell-dropdown-cancel" onClick={()=>setEditing(false)}>취소</button></div>:editing&&<input autoFocus className="cell-editor" style={{left:inputLeft,top:inputTop,width:inputWidth,height:inputHeight}} value={draft} onChange={(event)=>setDraft(event.target.value)} onBlur={()=>commit(draft)} onKeyDown={(event)=>{if(event.key==='Enter'){event.preventDefault();commit(draft)}else if(event.key==='Escape'){setEditing(false);setDraft(activeText)}}}/>}
    <div className="sr-only" aria-live="polite">선택 범위 {selectionAddress}, 활성 셀 값 {activeText||'비어 있음'}{activeCell?.spill_source?`, ${activeCell.spill_source} 배열 수식 결과`:''}{fillPreview?`, 자동 채우기 미리보기 ${address(fillPreview.startRow,fillPreview.startColumn)}:${address(fillPreview.endRow,fillPreview.endColumn)}`:''}</div>
  </div>
}
