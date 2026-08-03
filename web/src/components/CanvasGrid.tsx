import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowDownAZ, ArrowUpAZ, BadgeCheck, BarChart3, Clipboard, ClipboardPaste, Copy, Eraser, EyeOff, Filter, Link2, MessageSquarePlus, Palette, PanelTop, Rows3, Scissors, Table2, Trash2 } from 'lucide-react'
import { api, address, newIdempotencyKey } from '../lib/api'
import { ContextMenu, type MenuItem } from './ContextMenu'
import type { LayoutCommand } from './LayoutDialog'
import type { StructureCommand } from './StructureDialog'
import { dataRegion, looksLikeHeaderRow } from '../lib/dataRegion'
import { clampDimensionSize, pointerRegion, resizeHandleAt, type GridGeometry, type ResizeTarget } from '../lib/gridGeometry'
import { clipboardText, KANPIC_CLIPBOARD_TYPE, materializeFill, MAX_GRID_COLUMNS, MAX_GRID_ROWS, MAX_PASTE_CELLS, type FillRange, type KanpicClipboard, type PastedCell } from '../lib/clipboard'
import { collaborationClientId } from '../lib/client'
import { cellMerge,selectedMergedBounds,stripMergeStyle,type MergeRange } from '../lib/merge'
import { enqueue, flushOutbox } from '../lib/outbox'
import { axisIndexAtViewport,axisViewportPosition,createDimensionAxis,type DimensionAxis } from '../lib/dimensionAxis'
import { formatCellValue,wrapText,type CellBorders,type BorderSide } from '../lib/cellFormat'
import { optionForValue,optionLabel,validateClientInputs,validateClientValue,validationForCell } from '../lib/validation'
import { presenceColor, useCollaborationStore } from '../state/collaboration'
import { cellKey, selectedBounds, useEditorStore } from '../state/editor'
import type { Cell, ConditionalFormat, ConditionalFormatCell, ConditionalFormatEvaluation, DataValidation, MutationResult, SheetLayout } from '../types'

const HEADER_WIDTH=46
const HEADER_HEIGHT=27
const TOTAL_ROWS=MAX_GRID_ROWS
const TOTAL_COLUMNS=MAX_GRID_COLUMNS

export type GridShortcut=
  | {command:'fill-down'|'fill-right'|'select-all'|'select-row'|'select-column'|'move-first'|'move-last'}
  | {command:'select-data-region'|'clear-contents'|'auto-sum'|'insert-today'|'insert-now'|'copy'|'cut'|'paste'|'paste-values'}
  | {command:'move-data-edge';direction:'up'|'down'|'left'|'right';extend:boolean}
  | {command:'move-page';direction:'up'|'down'|'left'|'right';extend:boolean}

/** Menu actions the editor page owns because they open dialogs or panels. */
export type GridMenuCommand=
  | {command:'sort-dialog'|'filter'|'comment'|'named-range'|'conditional-format'|'data-validation'|'chart'|'pivot'|'format-dialog'|'layout-dialog'|'structure-dialog'|'clear-format'|'find-replace'}
  | {command:'merge';merge:boolean}
  | {command:'sort-region';column:number;direction:'asc'|'desc';region:{startRow:number;startColumn:number;endRow:number;endColumn:number};headerRows:number}

const RESIZE_HANDLE=4,MIN_ROW_HEIGHT=16,MAX_ROW_HEIGHT=400,MIN_COLUMN_WIDTH=32,MAX_COLUMN_WIDTH=600,DEFAULT_ROW_HEIGHT=27,DEFAULT_COLUMN_WIDTH=108

function columnName(column:number){let value=column,result='';while(value){value--;result=String.fromCharCode(65+value%26)+result;value=Math.floor(value/26)}return result}
function parsedValue(raw:string):unknown{if(raw==='')return undefined;if(raw.toLowerCase()==='true')return true;if(raw.toLowerCase()==='false')return false;if(Number.isFinite(Number(raw))&&raw.trim()!=='')return Number(raw);return raw}
function parsedAddress(value:string){const match=/^([A-Z]+)([1-9]\d*)$/.exec(value.toUpperCase());if(!match)return;let column=0;for(const character of match[1])column=column*26+character.charCodeAt(0)-64;return{row:Number(match[2]),column}}
function formulaPreview(value:unknown){if(!Array.isArray(value))return value;const first=value[0];return Array.isArray(first)?first[0]:first}
const DEFAULT_LAYOUT:SheetLayout={revision:1,frozen_rows:0,frozen_columns:0}
function indexesIn(axis:DimensionAxis,start:number,end:number){const result:number[]=[];if(end<start)return result;let index=axis.firstVisibleAtOrAfter(start);while(index<=end&&result.length<10000){result.push(index);const next=axis.nextVisible(index,1);if(next<=index)break;index=next}return result}
function paintCellBorders(context:CanvasRenderingContext2D,borders:CellBorders,x:number,y:number,width:number,height:number,zoom:number){
  const line=(side:'top'|'right'|'bottom'|'left',definition:BorderSide,offset=0)=>{const position=offset+.5;context.beginPath();if(side==='top'){context.moveTo(x,y+position);context.lineTo(x+width,y+position)}else if(side==='right'){context.moveTo(x+width-position,y);context.lineTo(x+width-position,y+height)}else if(side==='bottom'){context.moveTo(x,y+height-position);context.lineTo(x+width,y+height-position)}else{context.moveTo(x+position,y);context.lineTo(x+position,y+height)}context.strokeStyle=definition.color;context.lineWidth=Math.max(1,(definition.style==='medium'?2:definition.style==='thick'?3:1)*zoom);context.setLineDash(definition.style==='dashed'?[6*zoom,3*zoom]:definition.style==='dotted'?[2*zoom,2*zoom]:[]);context.stroke()}
  context.save();for(const side of ['top','right','bottom','left'] as const){const definition=borders[side];if(!definition)continue;if(definition.style==='double'){line(side,definition,1);line(side,definition,4)}else line(side,definition)}context.restore()
}

export function CanvasGrid({sheetId,layout=DEFAULT_LAYOUT,version,onVersion,hiddenRows=[],validations=[],conditionalFormats=[],showFormulas=false,onLayout,onStructure,onMenuCommand}:{sheetId:string;layout?:SheetLayout;version:number;onVersion:(version:number)=>void;hiddenRows?:number[];validations?:DataValidation[];conditionalFormats?:ConditionalFormat[];showFormulas?:boolean;onLayout?:(command:LayoutCommand)=>Promise<void>;onStructure?:(command:StructureCommand)=>Promise<void>;onMenuCommand?:(command:GridMenuCommand)=>void}) {
  const viewport=useRef<HTMLDivElement>(null),canvas=useRef<HTMLCanvasElement>(null),dragging=useRef(false),filling=useRef(false),fillPreviewRef=useRef<FillRange|undefined>(undefined),pasteAsValues=useRef(false)
  const headerDrag=useRef<{axis:'row'|'column';anchor:number}|null>(null),resizeDrag=useRef<{axis:'row'|'column';index:number;origin:number;start:number;count:number;size:number}|null>(null),internalClipboard=useRef<KanpicClipboard|undefined>(undefined)
  const [scroll,setScroll]=useState({left:0,top:0}),[size,setSize]=useState({width:900,height:500}),[draft,setDraft]=useState(''),[fillPreview,setFillPreview]=useState<FillRange>(),[refreshToken,setRefreshToken]=useState(0),[conditionalCells,setConditionalCells]=useState<Map<string,ConditionalFormatCell>>(()=>new Map())
  const [resizePreview,setResizePreview]=useState<{axis:'row'|'column';index:number;size:number}>(),[menu,setMenu]=useState<{x:number;y:number;items:MenuItem[];label:string}>()
  const editor=useEditorStore()
  const {activeRow,activeColumn,anchorRow,anchorColumn,editing,zoom,cells,select,setEditing,replaceRange,putCells,putCell,setSaveState,recordOperation}=editor
  const selection=selectedMergedBounds(cells,selectedBounds(editor))
  const collaborators=useCollaborationStore(state=>state.users)
  const sendCursor=useCollaborationStore(state=>state.sendCursor),sendSelection=useCollaborationStore(state=>state.sendSelection)
  const hiddenRowsKey=hiddenRows.join(','),layoutKey=JSON.stringify(layout),conditionalRulesKey=conditionalFormats.map(rule=>`${rule.id}:${rule.revision}`).join(',')
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
  useEffect(()=>{const element=viewport.current;if(!element)return;let left=element.scrollLeft,top=element.scrollTop;const bodyWidth=Math.max(1,element.clientWidth-HEADER_WIDTH),bodyHeight=Math.max(1,element.clientHeight-HEADER_HEIGHT),frozenWidth=columnAxis.offsetOf(frozenColumns+1),frozenHeight=rowAxis.offsetOf(frozenRows+1);if(activeColumn>frozenColumns){const start=columnAxis.offsetOf(activeColumn),end=start+columnAxis.sizeOf(activeColumn),visibleStart=left+frozenWidth,visibleEnd=left+bodyWidth;if(start<visibleStart)left=Math.max(0,start-frozenWidth);else if(end>visibleEnd)left=Math.max(0,end-bodyWidth)}if(activeRow>frozenRows){const start=rowAxis.offsetOf(activeRow),end=start+rowAxis.sizeOf(activeRow),visibleStart=top+frozenHeight,visibleEnd=top+bodyHeight;if(start<visibleStart)top=Math.max(0,start-frozenHeight);else if(end>visibleEnd)top=Math.max(0,end-bodyHeight)}if(left!==element.scrollLeft||top!==element.scrollTop){element.scrollLeft=left;element.scrollTop=top;setScroll({left,top})}},[activeRow,activeColumn,sheetId,rowAxis,columnAxis,frozenRows,frozenColumns])

  const visibleRange=useMemo(()=>{const frozenHeight=rowAxis.offsetOf(frozenRows+1),frozenWidth=columnAxis.offsetOf(frozenColumns+1),startRow=rowAxis.firstVisibleAtOrAfter(Math.max(frozenRows+1,rowAxis.indexAtOffset(scroll.top+frozenHeight))),startColumn=columnAxis.firstVisibleAtOrAfter(Math.max(frozenColumns+1,columnAxis.indexAtOffset(scroll.left+frozenWidth))),endRow=rowAxis.lastVisibleAtOrBefore(rowAxis.indexAtOffset(scroll.top+Math.max(frozenHeight,size.height-HEADER_HEIGHT)+100*zoom)),endColumn=columnAxis.lastVisibleAtOrBefore(columnAxis.indexAtOffset(scroll.left+Math.max(frozenWidth,size.width-HEADER_WIDTH)+250*zoom));return{startRow,startColumn,endRow:Math.max(startRow,endRow),endColumn:Math.max(startColumn,endColumn)}},[scroll,size,rowAxis,columnAxis,frozenRows,frozenColumns,zoom])
  useEffect(()=>{const controller=new AbortController(),ranges=[visibleRange];if(frozenRows>0)ranges.push({startRow:rowAxis.firstVisibleAtOrAfter(1),endRow:rowAxis.lastVisibleAtOrBefore(frozenRows),startColumn:visibleRange.startColumn,endColumn:visibleRange.endColumn});if(frozenColumns>0)ranges.push({startRow:visibleRange.startRow,endRow:visibleRange.endRow,startColumn:columnAxis.firstVisibleAtOrAfter(1),endColumn:columnAxis.lastVisibleAtOrBefore(frozenColumns)});if(frozenRows>0&&frozenColumns>0)ranges.push({startRow:rowAxis.firstVisibleAtOrAfter(1),endRow:rowAxis.lastVisibleAtOrBefore(frozenRows),startColumn:columnAxis.firstVisibleAtOrAfter(1),endColumn:columnAxis.lastVisibleAtOrBefore(frozenColumns)});for(const selected of ranges){if(selected.endRow<selected.startRow||selected.endColumn<selected.startColumn)continue;const range=`${address(selected.startRow,selected.startColumn)}:${address(selected.endRow,selected.endColumn)}`;api<{items:Cell[]}>(`/api/v1/sheets/${sheetId}/ranges/${range}`,{signal:controller.signal}).then(result=>replaceRange(result.items,selected.startRow,selected.startColumn,selected.endRow,selected.endColumn)).catch(()=>{})}return()=>controller.abort()},[sheetId,version,refreshToken,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn,frozenRows,frozenColumns,rowAxis,columnAxis,replaceRange])
  useEffect(()=>{
    if(conditionalFormats.length===0){setConditionalCells(new Map());return}
    const controller=new AbortController(),ranges=[visibleRange]
    if(frozenRows>0)ranges.push({startRow:rowAxis.firstVisibleAtOrAfter(1),endRow:rowAxis.lastVisibleAtOrBefore(frozenRows),startColumn:visibleRange.startColumn,endColumn:visibleRange.endColumn})
    if(frozenColumns>0)ranges.push({startRow:visibleRange.startRow,endRow:visibleRange.endRow,startColumn:columnAxis.firstVisibleAtOrAfter(1),endColumn:columnAxis.lastVisibleAtOrBefore(frozenColumns)})
    if(frozenRows>0&&frozenColumns>0)ranges.push({startRow:rowAxis.firstVisibleAtOrAfter(1),endRow:rowAxis.lastVisibleAtOrBefore(frozenRows),startColumn:columnAxis.firstVisibleAtOrAfter(1),endColumn:columnAxis.lastVisibleAtOrBefore(frozenColumns)})
    const requests=ranges.filter(selected=>selected.endRow>=selected.startRow&&selected.endColumn>=selected.startColumn).map(selected=>{const range=`${address(selected.startRow,selected.startColumn)}:${address(selected.endRow,selected.endColumn)}`;return api<ConditionalFormatEvaluation>(`/api/v1/sheets/${sheetId}/conditional-formats:evaluate?range=${encodeURIComponent(range)}`,{signal:controller.signal})})
    Promise.all(requests).then(results=>{const next=new Map<string,ConditionalFormatCell>();for(const result of results)for(const item of result.items)next.set(cellKey(item.row,item.column),item);setConditionalCells(next)}).catch(()=>{})
    return()=>controller.abort()
  },[sheetId,version,refreshToken,conditionalRulesKey,visibleRange.startRow,visibleRange.startColumn,visibleRange.endRow,visibleRange.endColumn,frozenRows,frozenColumns,rowAxis,columnAxis])

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
    const wholeColumns=selection.startRow<=1&&selection.endRow>=TOTAL_ROWS,wholeRows=selection.startColumn<=1&&selection.endColumn>=TOTAL_COLUMNS
    for(const column of columns){const x=columnPosition(column),width=columnAxis.sizeOf(column);if(x+width<HEADER_WIDTH||x>size.width)continue
      const selected=column>=selection.startColumn&&column<=selection.endColumn
      context.fillStyle=selected&&wholeColumns?'#c7e3dd':selected?'#e6f2ef':'#f7f9fb';context.fillRect(x,0,width,HEADER_HEIGHT)
      context.beginPath();context.moveTo(Math.round(x)+.5,0);context.lineTo(Math.round(x)+.5,size.height);context.stroke()
      if(selected){context.fillStyle='#0f766e';context.fillRect(x,HEADER_HEIGHT-2,width,2)}
      context.fillStyle=selected?'#0b5c55':'#52606d';context.font=`${selected?'600 ':''}${12*zoom}px Inter, Pretendard, sans-serif`;context.textAlign='center';context.fillText(columnName(column),x+width/2,HEADER_HEIGHT/2)}
    context.font=`${12*zoom}px Inter, Pretendard, sans-serif`
    for(const row of rows){const y=rowPosition(row),height=rowAxis.sizeOf(row);if(y+height<HEADER_HEIGHT||y>size.height)continue
      const selected=row>=selection.startRow&&row<=selection.endRow
      context.fillStyle=selected&&wholeRows?'#c7e3dd':selected?'#e6f2ef':'#f7f9fb';context.fillRect(0,y,HEADER_WIDTH,height)
      context.beginPath();context.moveTo(0,Math.round(y)+.5);context.lineTo(size.width,Math.round(y)+.5);context.stroke()
      if(selected){context.fillStyle='#0f766e';context.fillRect(HEADER_WIDTH-2,y,2,height)}
      context.fillStyle=selected?'#0b5c55':'#73808c';context.font=`${selected?'600 ':''}${12*zoom}px Inter, Pretendard, sans-serif`;context.textAlign='right';context.fillText(String(row),HEADER_WIDTH-8,y+height/2)}
    context.font=`${12*zoom}px Inter, Pretendard, sans-serif`
    context.save();context.beginPath();context.rect(HEADER_WIDTH,HEADER_HEIGHT,size.width-HEADER_WIDTH,size.height-HEADER_HEIGHT);context.clip()
    const mergedRanges=new Map<string,{range:MergeRange;representative:Cell}>()
    const drawCell=(cell:Cell,x:number,y:number,width:number,height:number)=>{
      const conditional=conditionalCells.get(cellKey(cell.row,cell.column)),style={...(cell.style??{}),...(conditional?.style??{})},validation=validationForCell(validations,cell.row,cell.column),validationOption=validation?.rule_type==='list'?optionForValue(validation,cell.value):undefined
      context.fillStyle=typeof style.background==='string'?style.background:'#fff';context.fillRect(x+1,y+1,width-2,height-2)
      if(conditional?.data_bar){context.save();context.globalAlpha=.3;context.fillStyle=conditional.data_bar.color;context.fillRect(x+3,y+4,Math.max(0,(width-6)*conditional.data_bar.ratio),Math.max(0,height-8));context.restore()}
      if(validation?.display_style==='chip'&&validationOption?.color){context.fillStyle=validationOption.color;context.beginPath();context.roundRect(x+4,y+4,width-8,height-8,6);context.fill()}
      const formulaError=typeof cell.value==='string'&&cell.value.startsWith('#')
      const fontSize=typeof style.font_size==='number'?style.font_size:12,fontFamily=typeof style.font_family==='string'?JSON.stringify(style.font_family):'Inter, Pretendard, sans-serif'
      context.fillStyle=formulaError?'#c2413b':typeof style.color==='string'?style.color:'#1c2733';context.font=`${style.italic===true?'italic ':''}${style.bold||formulaError?'600':'400'} ${fontSize*zoom}px ${fontFamily}`
      const alignment=validation?.display_style==='chip'?'left':style.horizontal_align==='left'||style.horizontal_align==='center'||style.horizontal_align==='right'?style.horizontal_align:typeof cell.value==='number'?'right':'left'
      context.textAlign=alignment
      const text=showFormulas&&cell.formula?cell.formula:validationOption?optionLabel(validationOption):formatCellValue(cell.value,style),textX=alignment==='right'?x+width-7:alignment==='center'?x+width/2:x+(validation?.display_style==='chip'?10:7)
      const vertical=style.vertical_align==='top'||style.vertical_align==='bottom'||style.vertical_align==='middle'?style.vertical_align:'middle'
      const textY=vertical==='top'?y+Math.max(4,fontSize*zoom/2+3):vertical==='bottom'?y+height-Math.max(4,fontSize*zoom/2+3):y+height/2
      const rotation=typeof style.text_rotation==='number'?style.text_rotation:0,maxTextWidth=Math.max(0,width-12),textMode=style.text_mode==='wrap'||style.wrap===true?'wrap':style.text_mode==='clip'?'clip':'overflow'
      context.save();if(textMode!=='overflow'||rotation!==0){context.beginPath();context.rect(x+1,y+1,Math.max(0,width-2),Math.max(0,height-2));context.clip()}
      if(textMode==='wrap'&&rotation===0){const lines=wrapText(text,maxTextWidth,value=>context.measureText(value).width),lineHeight=Math.max(fontSize*zoom*1.25,12*zoom),visibleLines=Math.max(1,Math.floor((height-6)/lineHeight)),shown=lines.slice(0,visibleLines),blockHeight=shown.length*lineHeight,startY=vertical==='top'?y+3+lineHeight/2:vertical==='bottom'?y+height-3-blockHeight+lineHeight/2:y+(height-blockHeight)/2+lineHeight/2;shown.forEach((line,index)=>context.fillText(line,textX,startY+index*lineHeight,maxTextWidth))}else{context.translate(textX,textY);if(rotation)context.rotate(rotation*Math.PI/180);context.fillText(text,0,0,maxTextWidth);if(text&&(style.underline===true||style.strike===true)){const measured=Math.min(context.measureText(text).width,maxTextWidth),start=alignment==='right'?-measured:alignment==='center'?-measured/2:0;context.strokeStyle=context.fillStyle;context.lineWidth=Math.max(1,zoom);if(style.underline===true){context.beginPath();context.moveTo(start,fontSize*zoom*.48);context.lineTo(start+measured,fontSize*zoom*.48);context.stroke()}if(style.strike===true){context.beginPath();context.moveTo(start,0);context.lineTo(start+measured,0);context.stroke()}}}
      context.restore();if(style.borders&&typeof style.borders==='object')paintCellBorders(context,style.borders as CellBorders,x,y,width,height,zoom)
      if(validation?.rule_type==='list'&&validation.show_dropdown&&validation.display_style!=='plain'){context.fillStyle='#52606d';context.beginPath();context.moveTo(x+width-13,y+height/2-2);context.lineTo(x+width-7,y+height/2-2);context.lineTo(x+width-10,y+height/2+2);context.closePath();context.fill()}
      if(validation){const checked=validateClientValue(validation,cell.value);if(!checked.valid&&!checked.deferred){context.fillStyle='#dc2626';context.beginPath();context.moveTo(x+width-9,y+1);context.lineTo(x+width-1,y+1);context.lineTo(x+width-1,y+9);context.closePath();context.fill()}}
      if(cell.spill_source){context.save();context.setLineDash([2,2]);context.strokeStyle='#38a3a5';context.lineWidth=1;context.strokeRect(Math.round(x)+2,Math.round(y)+2,Math.round(width)-4,Math.round(height)-4);context.restore()}
    }
    const renderCells=new Map(cells);conditionalCells.forEach(item=>{const key=cellKey(item.row,item.column);if(!renderCells.has(key))renderCells.set(key,{sheet_id:sheetId,row:item.row,column:item.column,updated_at:''})})
    const visibleCells=Array.from(renderCells.values()).filter(cell=>rowVisible(cell.row)&&columnVisible(cell.column)).sort((a,b)=>Number(a.row<=frozenRows)+Number(a.column<=frozenColumns)-Number(b.row<=frozenRows)-Number(b.column<=frozenColumns))
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
    if(resizePreview){
      context.save();context.strokeStyle='#0f766e';context.lineWidth=2;context.setLineDash([6,4])
      if(resizePreview.axis==='column'){const x=columnPosition(resizePreview.index)+resizePreview.size*zoom;context.beginPath();context.moveTo(Math.round(x)+.5,0);context.lineTo(Math.round(x)+.5,size.height)}
      else{const y=rowPosition(resizePreview.index)+resizePreview.size*zoom;context.beginPath();context.moveTo(0,Math.round(y)+.5);context.lineTo(size.width,Math.round(y)+.5)}
      context.stroke();context.restore()
      const label=`${Math.round(resizePreview.size)}px`
      context.font=`600 ${10*zoom}px Inter, Pretendard, sans-serif`;context.textAlign='left'
      const labelWidth=context.measureText(label).width+12
      const labelX=resizePreview.axis==='column'?Math.min(size.width-labelWidth-4,columnPosition(resizePreview.index)+resizePreview.size*zoom+6):HEADER_WIDTH+6
      const labelY=resizePreview.axis==='column'?HEADER_HEIGHT+6:Math.min(size.height-22,rowPosition(resizePreview.index)+resizePreview.size*zoom+6)
      context.fillStyle='#0f766e';context.beginPath();context.roundRect(labelX,labelY,labelWidth,18,5);context.fill()
      context.fillStyle='#fff';context.fillText(label,labelX+6,labelY+9)
    }
  },[size,scroll,rowAxis,columnAxis,frozenRows,frozenColumns,cells,conditionalCells,activeRow,activeColumn,activeCell,zoom,visibleRange,collaborators,sheetId,selection.startRow,selection.startColumn,selection.endRow,selection.endColumn,fillPreview,validations,showFormulas,resizePreview])

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
    return saveCell(value,formula,row,column)
  },[activeRow,activeColumn,cells,saveCell])

  useEffect(()=>{const sync=()=>flushOutbox(handleApplied);window.addEventListener('online',sync);const timer=window.setInterval(sync,3000);sync();return()=>{window.removeEventListener('online',sync);window.clearInterval(timer)}},[handleApplied])

  const selectionPayload=useCallback((range:FillRange=selection)=>{
    const rows=range.endRow-range.startRow+1,columns=range.endColumn-range.startColumn+1
    if(rows*columns>MAX_PASTE_CELLS)throw new Error(`복사와 잘라내기는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`)
    const payload:KanpicClipboard={version:1,sourceRow:range.startRow,sourceColumn:range.startColumn,rows,columns,cells:[]}
    for(let row=range.startRow;row<=range.endRow;row+=1)for(let column=range.startColumn;column<=range.endColumn;column+=1){
      const cell=cells.get(cellKey(row,column))
      payload.cells.push({rowOffset:row-range.startRow,columnOffset:column-range.startColumn,value:cell?.value,formula:cell?.formula||undefined,style:stripMergeStyle(cell?.style)})
    }
    return payload
  },[cells,selection])
  const fillSelection=useCallback(async(target:FillRange)=>{try{const inputs=materializeFill(selectionPayload(),target);await queueCells(inputs,'fill')}catch(error){setSaveState('error');alert(error instanceof Error?error.message:'자동 채우기를 적용하지 못했습니다.')}},[queueCells,selectionPayload,setSaveState])
  const fillFrom=useCallback(async(source:FillRange)=>{try{const inputs=materializeFill(selectionPayload(source),selection);await queueCells(inputs,'fill')}catch(error){setSaveState('error');alert(error instanceof Error?error.message:'선택 범위를 채우지 못했습니다.')}},[queueCells,selection,selectionPayload,setSaveState])
  const fillDown=useCallback(()=>void fillFrom({startRow:selection.startRow,startColumn:selection.startColumn,endRow:selection.startRow,endColumn:selection.endColumn}),[fillFrom,selection])
  const fillRight=useCallback(()=>void fillFrom({startRow:selection.startRow,startColumn:selection.startColumn,endRow:selection.endRow,endColumn:selection.startColumn}),[fillFrom,selection])
  const fillDraft=useCallback(async(raw:string)=>{try{const formula=raw.startsWith('=')?raw:undefined,current=cells.get(cellKey(activeRow,activeColumn)),payload:KanpicClipboard={version:1,sourceRow:activeRow,sourceColumn:activeColumn,rows:1,columns:1,cells:[{rowOffset:0,columnOffset:0,value:formula?undefined:parsedValue(raw),formula,style:stripMergeStyle(current?.style)}]},inputs=[{row:activeRow,column:activeColumn,value:formula?undefined:parsedValue(raw),formula,style:current?.style},...materializeFill(payload,selection)];await queueCells(inputs,'fill');setEditing(false)}catch(error){setSaveState('error');alert(error instanceof Error?error.message:'선택 범위에 값을 채우지 못했습니다.')}},[activeColumn,activeRow,cells,queueCells,selection,setEditing,setSaveState])

  const selectCell=useCallback((row:number,column:number,extend=false)=>{
    let nextRow=Math.max(1,Math.min(TOTAL_ROWS,row)),nextColumn=Math.max(1,Math.min(TOTAL_COLUMNS,column));if(rowAxis.isHidden(nextRow))nextRow=row>=activeRow?rowAxis.firstVisibleAtOrAfter(nextRow):rowAxis.lastVisibleAtOrBefore(nextRow);if(columnAxis.isHidden(nextColumn))nextColumn=column>=activeColumn?columnAxis.firstVisibleAtOrAfter(nextColumn):columnAxis.lastVisibleAtOrBefore(nextColumn)
    const merged=!extend?cellMerge(cells.get(cellKey(nextRow,nextColumn))):undefined
    if(merged){nextRow=merged.startRow;nextColumn=merged.startColumn}
    select(nextRow,nextColumn,extend);sendCursor({sheet_id:sheetId,row:nextRow,column:nextColumn})
    const start=extend?{row:Math.min(anchorRow,nextRow),column:Math.min(anchorColumn,nextColumn)}:{row:merged?.startRow??nextRow,column:merged?.startColumn??nextColumn}
    const end=extend?{row:Math.max(anchorRow,nextRow),column:Math.max(anchorColumn,nextColumn)}:{row:merged?.endRow??nextRow,column:merged?.endColumn??nextColumn}
    sendSelection({sheet_id:sheetId,start,end})
  },[anchorRow,anchorColumn,cells,select,sendCursor,sendSelection,sheetId,rowAxis,columnAxis,activeRow,activeColumn])
  // Keeping the active cell on a caller-chosen corner stops whole-row and
  // whole-column selections from scrolling to the end of the grid.
  const selectSpan=useCallback((startRow:number,startColumn:number,endRow:number,endColumn:number,active?:{row:number;column:number})=>{
    const target=active??{row:endRow,column:endColumn}
    const anchor={row:target.row===startRow?endRow:startRow,column:target.column===startColumn?endColumn:startColumn}
    select(anchor.row,anchor.column);select(target.row,target.column,true)
    sendCursor({sheet_id:sheetId,row:target.row,column:target.column})
    sendSelection({sheet_id:sheetId,start:{row:startRow,column:startColumn},end:{row:endRow,column:endColumn}})
  },[select,sendCursor,sendSelection,sheetId])
  const selectRange=useCallback((startRow:number,startColumn:number,endRow:number,endColumn:number)=>selectSpan(startRow,startColumn,endRow,endColumn,{row:startRow,column:startColumn}),[selectSpan])
  const populated=useCallback((row:number,column:number)=>{const cell=cells.get(cellKey(row,column));return cell?.value!=null||Boolean(cell?.formula)},[cells])
  const moveDataEdge=useCallback((direction:'up'|'down'|'left'|'right',extend:boolean)=>{const rowStep=direction==='up'?-1:direction==='down'?1:0,columnStep=direction==='left'?-1:direction==='right'?1:0,filled=populated(activeRow,activeColumn);let row=activeRow,column=activeColumn;for(let index=0;index<TOTAL_ROWS+TOTAL_COLUMNS;index+=1){const nextRow=rowStep?rowAxis.nextVisible(row,rowStep):row,nextColumn=columnStep?columnAxis.nextVisible(column,columnStep):column;if(nextRow===row&&nextColumn===column)break;if(populated(nextRow,nextColumn)!==filled)break;row=nextRow;column=nextColumn}selectCell(row,column,extend)},[activeColumn,activeRow,columnAxis,populated,rowAxis,selectCell])
  // The commit is asynchronous, so only advance the cursor when the user has
  // not already selected somewhere else while the write was in flight.
  const commitAndMove=(rowOffset:number,columnOffset:number)=>{
    const row=activeRow,column=activeColumn
    void commit(draft).then(saved=>{
      if(!saved)return
      const current=useEditorStore.getState()
      if(current.activeRow!==row||current.activeColumn!==column||current.anchorRow!==row||current.anchorColumn!==column)return
      selectCell(rowOffset===0?row:rowAxis.nextVisible(row,rowOffset>0?1:-1),columnOffset===0?column:columnAxis.nextVisible(column,columnOffset>0?1:-1))
    })
  }
  const pointerPosition=(event:{clientX:number;clientY:number})=>{const rect=canvas.current!.getBoundingClientRect();return{x:event.clientX-rect.left,y:event.clientY-rect.top}}
  const pointCell=(event:React.PointerEvent<HTMLCanvasElement>)=>{const{x,y}=pointerPosition(event);if(x<HEADER_WIDTH||y<HEADER_HEIGHT)return;return{row:axisIndexAtViewport(rowAxis,y-HEADER_HEIGHT,scroll.top,frozenRows),column:axisIndexAtViewport(columnAxis,x-HEADER_WIDTH,scroll.left,frozenColumns)}}
  const geometry:GridGeometry={rowAxis,columnAxis,scroll,frozenRows,frozenColumns,headerWidth:HEADER_WIDTH,headerHeight:HEADER_HEIGHT}
  const regionAt=(x:number,y:number)=>pointerRegion(geometry,x,y)
  const resizeTargetAt=(x:number,y:number)=>resizeHandleAt(geometry,x,y,RESIZE_HANDLE)
  const wholeColumnsSelected=selection.startRow<=1&&selection.endRow>=TOTAL_ROWS,wholeRowsSelected=selection.startColumn<=1&&selection.endColumn>=TOTAL_COLUMNS
  // Dragging a boundary inside a whole-row or whole-column selection resizes
  // every selected dimension at once, like Sheets and Excel do.
  const resizeSpan=(target:ResizeTarget)=>{
    if(target.axis==='column'&&wholeColumnsSelected&&target.index>=selection.startColumn&&target.index<=selection.endColumn)return{start:selection.startColumn,count:selection.endColumn-selection.startColumn+1}
    if(target.axis==='row'&&wholeRowsSelected&&target.index>=selection.startRow&&target.index<=selection.endRow)return{start:selection.startRow,count:selection.endRow-selection.startRow+1}
    return{start:target.index,count:1}
  }
  const onFillHandle=(event:React.PointerEvent<HTMLCanvasElement>)=>{if(rowAxis.hidden.length>0||columnAxis.hidden.length>0)return false;const{x,y}=pointerPosition(event),handleX=HEADER_WIDTH+axisViewportPosition(columnAxis,selection.endColumn,scroll.left,frozenColumns)+columnAxis.sizeOf(selection.endColumn),handleY=HEADER_HEIGHT+axisViewportPosition(rowAxis,selection.endRow,scroll.top,frozenRows)+rowAxis.sizeOf(selection.endRow);return Math.abs(x-handleX)<=8&&Math.abs(y-handleY)<=8}
  const applyLayoutCommand=useCallback(async(command:LayoutCommand)=>{if(!onLayout)return;try{await onLayout(command)}catch{/* the editor page reports layout failures */}},[onLayout])
  const applyStructureCommand=useCallback(async(command:StructureCommand)=>{if(!onStructure)return;try{await onStructure(command)}catch{/* the editor page reports structure failures */}},[onStructure])
  const pointerDown=(event:React.PointerEvent<HTMLCanvasElement>)=>{
    if(event.button!==0)return
    setMenu(undefined)
    const{x,y}=pointerPosition(event),target=onLayout?resizeTargetAt(x,y):undefined
    if(target){
      const axis=target.axis==='column'?columnAxis:rowAxis,span=resizeSpan(target),size=Math.round(axis.sizeOf(target.index)/zoom)
      resizeDrag.current={axis:target.axis,index:target.index,origin:target.axis==='column'?event.clientX:event.clientY,start:span.start,count:span.count,size}
      setResizePreview({axis:target.axis,index:target.index,size})
      event.currentTarget.setPointerCapture(event.pointerId);event.preventDefault();viewport.current?.focus();return
    }
    const region=regionAt(x,y)
    if(region.kind==='corner'){selectSpan(1,1,TOTAL_ROWS,TOTAL_COLUMNS,{row:1,column:1});viewport.current?.focus();return}
    if(region.kind==='column'||region.kind==='row'){
      const anchor=event.shiftKey?(region.kind==='column'?anchorColumn:anchorRow):region.index
      headerDrag.current={axis:region.kind,anchor}
      if(region.kind==='column')selectSpan(1,Math.min(anchor,region.index),TOTAL_ROWS,Math.max(anchor,region.index),{row:1,column:region.index})
      else selectSpan(Math.min(anchor,region.index),1,Math.max(anchor,region.index),TOTAL_COLUMNS,{row:region.index,column:1})
      event.currentTarget.setPointerCapture(event.pointerId);event.preventDefault();viewport.current?.focus();return
    }
    if(onFillHandle(event)){filling.current=true;fillPreviewRef.current={...selection};setFillPreview({...selection});event.currentTarget.style.cursor='crosshair';event.currentTarget.setPointerCapture(event.pointerId);viewport.current?.focus();event.preventDefault();return}
    dragging.current=true;event.currentTarget.setPointerCapture(event.pointerId);selectCell(region.row,region.column,event.shiftKey);viewport.current?.focus()
  }
  const pointerMove=(event:React.PointerEvent<HTMLCanvasElement>)=>{
    const drag=resizeDrag.current
    if(drag){
      const axis=drag.axis==='column'?columnAxis:rowAxis
      const delta=((drag.axis==='column'?event.clientX:event.clientY)-drag.origin)/zoom
      const size=clampDimensionSize(drag.axis,axis.sizeOf(drag.index)/zoom+delta)
      setResizePreview({axis:drag.axis,index:drag.index,size});return
    }
    if(headerDrag.current){
      const{x,y}=pointerPosition(event),header=headerDrag.current
      if(header.axis==='column'){const index=axisIndexAtViewport(columnAxis,Math.max(0,x-HEADER_WIDTH),scroll.left,frozenColumns);selectSpan(1,Math.min(header.anchor,index),TOTAL_ROWS,Math.max(header.anchor,index),{row:1,column:index})}
      else{const index=axisIndexAtViewport(rowAxis,Math.max(0,y-HEADER_HEIGHT),scroll.top,frozenRows);selectSpan(Math.min(header.anchor,index),1,Math.max(header.anchor,index),TOTAL_COLUMNS,{row:index,column:1})}
      return
    }
    if(filling.current){const cell=pointCell(event);if(!cell)return;const next={startRow:Math.min(selection.startRow,cell.row),startColumn:Math.min(selection.startColumn,cell.column),endRow:Math.max(selection.endRow,cell.row),endColumn:Math.max(selection.endColumn,cell.column)};fillPreviewRef.current=next;setFillPreview(next);return}
    if(!dragging.current){
      const{x,y}=pointerPosition(event),target=onLayout?resizeTargetAt(x,y):undefined
      event.currentTarget.style.cursor=target?target.axis==='column'?'col-resize':'row-resize':onFillHandle(event)?'crosshair':'default'
      return
    }
    const cell=pointCell(event);if(cell)selectCell(cell.row,cell.column,true)
  }
  const finishGesture=(event:React.PointerEvent<HTMLCanvasElement>)=>{
    dragging.current=false;headerDrag.current=null;filling.current=false;fillPreviewRef.current=undefined;setFillPreview(undefined)
    event.currentTarget.style.cursor='default'
    if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId)
  }
  const pointerUp=(event:React.PointerEvent<HTMLCanvasElement>)=>{
    const drag=resizeDrag.current,preview=resizePreview
    resizeDrag.current=null
    const target=fillPreviewRef.current,shouldFill=filling.current
    finishGesture(event)
    if(drag){setResizePreview(undefined);if(preview&&preview.size!==drag.size)void applyLayoutCommand({action:'resize',axis:drag.axis,start:drag.start,count:drag.count,size:preview.size});return}
    if(shouldFill&&target)void fillSelection(target)
  }
  const pointerCancel=(event:React.PointerEvent<HTMLCanvasElement>)=>{resizeDrag.current=null;setResizePreview(undefined);finishGesture(event)}
  const editActiveCell=useCallback(()=>{if(activeCell?.spill_source){const source=parsedAddress(activeCell.spill_source);if(source){selectCell(source.row,source.column);setEditing(true);return}}setEditing(true)},[activeCell,selectCell,setEditing])
  const keyDown=(event:React.KeyboardEvent)=>{if(editing)return;const primary=event.ctrlKey||event.metaKey
    if(primary&&event.shiftKey&&event.key.toLowerCase()==='v'){pasteAsValues.current=true;return}
    if(primary&&event.key.toLowerCase()==='a'){selectRange(1,1,TOTAL_ROWS,TOTAL_COLUMNS);event.preventDefault()}
    else if(primary&&event.code==='Space'){selectRange(1,activeColumn,TOTAL_ROWS,activeColumn);event.preventDefault()}
    else if(event.shiftKey&&event.code==='Space'){selectRange(activeRow,1,activeRow,TOTAL_COLUMNS);event.preventDefault()}
    else if(primary&&['ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes(event.key)){const direction=event.key.replace('Arrow','').toLowerCase() as 'up'|'down'|'left'|'right';moveDataEdge(direction,event.shiftKey);event.preventDefault()}
    else if(primary&&!event.shiftKey&&event.key.toLowerCase()==='d'){fillDown();event.preventDefault()}
    else if(primary&&!event.shiftKey&&event.key.toLowerCase()==='r'){fillRight();event.preventDefault()}
    else if(primary&&event.key==='Home'){selectCell(1,1);event.preventDefault()}
    else if(primary&&event.key==='End'){selectCell(TOTAL_ROWS,TOTAL_COLUMNS);event.preventDefault()}
    else if(event.key==='Home'){selectCell(activeRow,columnAxis.firstVisibleAtOrAfter(1),event.shiftKey);event.preventDefault()}
    else if(event.key==='End'){moveDataEdge('right',event.shiftKey);event.preventDefault()}
    else if(event.key==='Enter'||event.key==='F2'){editActiveCell();event.preventDefault()}
    else if(event.key==='ArrowDown'){selectCell(rowAxis.nextVisible(activeRow,1),activeColumn,event.shiftKey);event.preventDefault()}
    else if(event.key==='ArrowUp'){selectCell(rowAxis.nextVisible(activeRow,-1),activeColumn,event.shiftKey);event.preventDefault()}
    else if(event.key==='ArrowRight'||event.key==='Tab'){selectCell(activeRow,columnAxis.nextVisible(activeColumn,event.key==='Tab'&&event.shiftKey?-1:1),event.shiftKey);event.preventDefault()}
    else if(event.key==='ArrowLeft'){selectCell(activeRow,columnAxis.nextVisible(activeColumn,-1),event.shiftKey);event.preventDefault()}
    else if(event.key==='ContextMenu'||(event.shiftKey&&event.key==='F10')){
      const rect=canvas.current?.getBoundingClientRect()
      setMenu({
        x:(rect?.left??0)+HEADER_WIDTH+axisViewportPosition(columnAxis,activeColumn,scroll.left,frozenColumns)+10,
        y:(rect?.top??0)+HEADER_HEIGHT+axisViewportPosition(rowAxis,activeRow,scroll.top,frozenRows)+10,
        items:cellMenuItems(selection),label:'셀 메뉴',
      })
      event.preventDefault()
    }
    else if(event.key==='Backspace'||event.key==='Delete'){const count=(selection.endRow-selection.startRow+1)*(selection.endColumn-selection.startColumn+1);if(count===1)void commit('');else void clearSelection();event.preventDefault()}
    else if(event.key.length===1&&!event.metaKey&&!event.ctrlKey&&!event.altKey){if(activeCell?.spill_source){const source=parsedAddress(activeCell.spill_source);if(source)selectCell(source.row,source.column);alert(`${address(activeRow,activeColumn)}은(는) ${activeCell.spill_source} 배열 수식의 결과입니다. 원본 수식 셀에서 입력하세요.`)}else{setDraft(event.key);setEditing(true)}event.preventDefault()}}
  const writeClipboard=(event:React.ClipboardEvent)=>{try{const payload=selectionPayload();internalClipboard.current=payload;event.preventDefault();event.clipboardData.setData('text/plain',clipboardText(payload));event.clipboardData.setData(KANPIC_CLIPBOARD_TYPE,JSON.stringify(payload));return true}catch(error){event.preventDefault();alert(error instanceof Error?error.message:'선택 범위를 복사하지 못했습니다.');return false}}
  const copy=(event:React.ClipboardEvent)=>{writeClipboard(event)}
  const clearSelection=useCallback(async()=>{
    const count=(selection.endRow-selection.startRow+1)*(selection.endColumn-selection.startColumn+1)
    if(count>MAX_PASTE_CELLS){alert(`잘라내기와 삭제는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`);return}
    const empty:PastedCell[]=[]
    for(let row=selection.startRow;row<=selection.endRow;row+=1)for(let column=selection.startColumn;column<=selection.endColumn;column+=1)empty.push({row,column})
    await queueCells(empty,'paste')
  },[queueCells,selection.endColumn,selection.endRow,selection.startColumn,selection.startRow])
  const cut=(event:React.ClipboardEvent)=>{if(writeClipboard(event))void clearSelection()}
  const runPaste=useCallback((text:string,internal:string,valuesOnly:boolean)=>{
    const worker=new Worker(new URL('../workers/paste.worker.ts',import.meta.url),{type:'module'})
    worker.onmessage=async(message:MessageEvent<{cells?:PastedCell[];error?:string}>)=>{try{if(message.data.error){setSaveState('error');alert(message.data.error);return}await queueCells(message.data.cells??[],'paste')}finally{worker.terminate()}}
    worker.onerror=()=>{setSaveState('error');worker.terminate();alert('붙여넣기 데이터를 처리하지 못했습니다.')}
    worker.postMessage({text,internal,startRow:activeRow,startColumn:activeColumn,valuesOnly})
  },[activeColumn,activeRow,queueCells,setSaveState])
  const paste=(event:React.ClipboardEvent)=>{
    event.preventDefault()
    const valuesOnly=pasteAsValues.current;pasteAsValues.current=false
    runPaste(event.clipboardData.getData('text/plain'),event.clipboardData.getData(KANPIC_CLIPBOARD_TYPE),valuesOnly)
  }
  // Menu-driven clipboard actions cannot rely on a browser clipboard event, so
  // they use the async Clipboard API and keep the last internal copy in memory
  // to preserve formulas and styles that plain text cannot carry.
  const copySelection=useCallback(async(cut:boolean)=>{
    try{
      const payload=selectionPayload(),text=clipboardText(payload)
      internalClipboard.current=payload
      if(navigator.clipboard?.writeText)await navigator.clipboard.writeText(text)
      else if(!document.execCommand('copy'))throw new Error('클립보드를 사용할 수 없습니다.')
      if(cut)await clearSelection()
    }catch(error){alert(error instanceof Error?error.message:'선택 범위를 복사하지 못했습니다.')}
  },[clearSelection,selectionPayload])
  const pasteFromClipboard=useCallback(async(valuesOnly:boolean)=>{
    try{
      if(!navigator.clipboard?.readText)throw new Error('이 브라우저에서는 Ctrl/⌘+V로 붙여넣어 주세요.')
      const text=await navigator.clipboard.readText()
      const cached=internalClipboard.current
      runPaste(text,cached&&clipboardText(cached)===text?JSON.stringify(cached):'',valuesOnly)
    }catch(error){alert(error instanceof Error?error.message:'클립보드를 읽지 못했습니다. Ctrl/⌘+V를 사용하세요.')}
  },[runPaste])
  const copySelectionLink=useCallback(async()=>{
    const parameters=new URLSearchParams({sheet_id:sheetId,range:selection.startRow===selection.endRow&&selection.startColumn===selection.endColumn?address(selection.startRow,selection.startColumn):`${address(selection.startRow,selection.startColumn)}:${address(selection.endRow,selection.endColumn)}`})
    const link=`${window.location.origin}${window.location.pathname}?${parameters.toString()}`
    try{await navigator.clipboard?.writeText(link)}catch{window.prompt('셀 링크를 복사하세요.',link)}
  },[selection,sheetId])
  // Moves by one visible screen, matching the Page Up and Page Down keys.
  const movePage=useCallback((direction:'up'|'down'|'left'|'right',extend:boolean)=>{
    if(direction==='up'||direction==='down'){
      const step=Math.max(1,Math.floor(Math.max(1,size.height-HEADER_HEIGHT-rowAxis.offsetOf(frozenRows+1))/Math.max(8,rowAxis.sizeOf(activeRow))))
      let row=activeRow
      for(let index=0;index<step;index+=1){const next=rowAxis.nextVisible(row,direction==='down'?1:-1);if(next===row)break;row=next}
      selectCell(row,activeColumn,extend);return
    }
    const step=Math.max(1,Math.floor(Math.max(1,size.width-HEADER_WIDTH-columnAxis.offsetOf(frozenColumns+1))/Math.max(16,columnAxis.sizeOf(activeColumn))))
    let column=activeColumn
    for(let index=0;index<step;index+=1){const next=columnAxis.nextVisible(column,direction==='right'?1:-1);if(next===column)break;column=next}
    selectCell(activeRow,column,extend)
  },[activeColumn,activeRow,columnAxis,frozenColumns,frozenRows,rowAxis,selectCell,size.height,size.width])
  // Sums the contiguous numbers above the active cell, then to its left, and
  // leaves the formula in the editor so it can be adjusted before committing.
  const autoSum=useCallback(()=>{
    let row=activeRow-1
    while(row>=1&&typeof cells.get(cellKey(row,activeColumn))?.value==='number')row-=1
    if(row<activeRow-1){setDraft(`=SUM(${address(row+1,activeColumn)}:${address(activeRow-1,activeColumn)})`);setEditing(true);return}
    let column=activeColumn-1
    while(column>=1&&typeof cells.get(cellKey(activeRow,column))?.value==='number')column-=1
    setDraft(column<activeColumn-1?`=SUM(${address(activeRow,column+1)}:${address(activeRow,activeColumn-1)})`:'=SUM()')
    setEditing(true)
  },[activeColumn,activeRow,cells,setEditing])
  useEffect(()=>{const shortcut=(event:Event)=>{
    const detail=(event as CustomEvent<GridShortcut>).detail
    if(!detail)return
    const pad=(value:number)=>String(value).padStart(2,'0'),now=new Date()
    switch(detail.command){
      case 'fill-down':fillDown();return
      case 'fill-right':fillRight();return
      case 'select-all':selectSpan(1,1,TOTAL_ROWS,TOTAL_COLUMNS,{row:1,column:1});return
      case 'select-row':selectSpan(activeRow,1,activeRow,TOTAL_COLUMNS,{row:activeRow,column:1});return
      case 'select-column':selectSpan(1,activeColumn,TOTAL_ROWS,activeColumn,{row:1,column:activeColumn});return
      case 'select-data-region':{const region=dataRegion(cells,activeRow,activeColumn,{rows:TOTAL_ROWS,columns:TOTAL_COLUMNS});selectSpan(region.startRow,region.startColumn,region.endRow,region.endColumn,{row:region.startRow,column:region.startColumn});return}
      case 'move-first':selectCell(1,1);return
      case 'move-last':selectCell(TOTAL_ROWS,TOTAL_COLUMNS);return
      case 'move-data-edge':moveDataEdge(detail.direction,detail.extend);return
      case 'move-page':movePage(detail.direction,detail.extend);return
      case 'clear-contents':void clearSelection();return
      case 'auto-sum':autoSum();return
      case 'insert-today':void commit(`${now.getFullYear()}-${pad(now.getMonth()+1)}-${pad(now.getDate())}`);return
      case 'insert-now':void commit(`${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`);return
      case 'copy':void copySelection(false);return
      case 'cut':void copySelection(true);return
      case 'paste':void pasteFromClipboard(false);return
      case 'paste-values':void pasteFromClipboard(true);return
    }
  };window.addEventListener('kanpic:grid-shortcut',shortcut);return()=>window.removeEventListener('kanpic:grid-shortcut',shortcut)},[activeColumn,activeRow,autoSum,cells,clearSelection,commit,copySelection,fillDown,fillRight,moveDataEdge,movePage,pasteFromClipboard,selectCell,selectSpan])
  // Auto-fit measures the cells already loaded in the client store, which is
  // the same data the canvas paints.
  const autoFitSize=(axis:'row'|'column',start:number,count:number)=>{
    const context=canvas.current?.getContext('2d')
    if(!context)return undefined
    const end=start+count-1
    let measured=0
    context.save()
    cells.forEach(cell=>{
      const index=axis==='column'?cell.column:cell.row
      if(index<start||index>end)return
      const style=(cell.style??{}) as Record<string,unknown>
      const fontSize=typeof style.font_size==='number'?style.font_size:12,fontFamily=typeof style.font_family==='string'?JSON.stringify(style.font_family):'Inter, Pretendard, sans-serif'
      context.font=`${style.italic===true?'italic ':''}${style.bold===true?'600':'400'} ${fontSize}px ${fontFamily}`
      const text=showFormulas&&cell.formula?cell.formula:formatCellValue(cell.value,style)
      if(!text)return
      if(axis==='column'){measured=Math.max(measured,context.measureText(text).width+18);return}
      const width=columnAxis.sizeOf(cell.column)/zoom
      const wrapped=style.text_mode==='wrap'||style.wrap===true?wrapText(text,Math.max(0,width-12),value=>context.measureText(value).width).length:1
      measured=Math.max(measured,wrapped*Math.max(fontSize*1.25,12)+9)
    })
    context.restore()
    if(measured===0)return axis==='column'?DEFAULT_COLUMN_WIDTH:DEFAULT_ROW_HEIGHT
    return clampDimensionSize(axis,measured)
  }
  const autoFit=(target:ResizeTarget)=>{
    const span=resizeSpan(target),size=autoFitSize(target.axis,span.start,span.count)
    if(size===undefined)return
    void applyLayoutCommand({action:'resize',axis:target.axis,start:span.start,count:span.count,size})
  }
  const doubleClick=(event:React.MouseEvent<HTMLCanvasElement>)=>{
    const{x,y}=pointerPosition(event),target=onLayout?resizeTargetAt(x,y):undefined
    if(target){autoFit(target);return}
    if(regionAt(x,y).kind==='cell')editActiveCell()
  }
  const sortByColumn=(column:number,direction:'asc'|'desc')=>{
    let seed:number|undefined
    cells.forEach(cell=>{if(cell.column===column&&(cell.value!=null||cell.formula)&&(seed===undefined||cell.row<seed))seed=cell.row})
    if(seed===undefined){alert('정렬할 데이터가 없습니다.');return}
    const region=dataRegion(cells,seed,column,{rows:TOTAL_ROWS,columns:TOTAL_COLUMNS})
    onMenuCommand?.({command:'sort-region',column,direction,region,headerRows:looksLikeHeaderRow(cells,region)?1:0})
  }
  const clipboardMenuItems=():MenuItem[]=>[
    {kind:'item',label:'잘라내기',shortcut:'Ctrl+X',icon:<Scissors/>,onSelect:()=>void copySelection(true)},
    {kind:'item',label:'복사',shortcut:'Ctrl+C',icon:<Copy/>,onSelect:()=>void copySelection(false)},
    {kind:'item',label:'붙여넣기',shortcut:'Ctrl+V',icon:<ClipboardPaste/>,onSelect:()=>void pasteFromClipboard(false)},
    {kind:'item',label:'값만 붙여넣기',shortcut:'Ctrl+Shift+V',icon:<Clipboard/>,onSelect:()=>void pasteFromClipboard(true)},
  ]
  const cellMenuItems=(range:FillRange):MenuItem[]=>{
    const rows=range.endRow-range.startRow+1,columns=range.endColumn-range.startColumn+1
    const merged=Boolean(cellMerge(cells.get(cellKey(range.startRow,range.startColumn))))
    const rowLabel=rows>1?`행 ${range.startRow}–${range.endRow}`:`행 ${range.startRow}`,columnLabel=columns>1?`열 ${columnName(range.startColumn)}–${columnName(range.endColumn)}`:`열 ${columnName(range.startColumn)}`
    return [...clipboardMenuItems(),{kind:'separator'},
      {kind:'item',label:`위에 행 ${rows}개 삽입`,disabled:!onStructure,onSelect:()=>void applyStructureCommand({axis:'row',action:'insert',index:range.startRow,count:rows})},
      {kind:'item',label:`아래에 행 ${rows}개 삽입`,disabled:!onStructure,onSelect:()=>void applyStructureCommand({axis:'row',action:'insert',index:range.endRow+1,count:rows})},
      {kind:'item',label:`왼쪽에 열 ${columns}개 삽입`,disabled:!onStructure,onSelect:()=>void applyStructureCommand({axis:'column',action:'insert',index:range.startColumn,count:columns})},
      {kind:'item',label:`오른쪽에 열 ${columns}개 삽입`,disabled:!onStructure,onSelect:()=>void applyStructureCommand({axis:'column',action:'insert',index:range.endColumn+1,count:columns})},
      {kind:'separator'},
      {kind:'item',label:`${rowLabel} 삭제`,icon:<Trash2/>,danger:true,disabled:!onStructure,onSelect:()=>{if(window.confirm(`${rowLabel}을(를) 삭제할까요?`))void applyStructureCommand({axis:'row',action:'delete',index:range.startRow,count:rows})}},
      {kind:'item',label:`${columnLabel} 삭제`,icon:<Trash2/>,danger:true,disabled:!onStructure,onSelect:()=>{if(window.confirm(`${columnLabel}을(를) 삭제할까요?`))void applyStructureCommand({axis:'column',action:'delete',index:range.startColumn,count:columns})}},
      {kind:'item',label:'내용 지우기',shortcut:'Delete',icon:<Eraser/>,onSelect:()=>void clearSelection()},
      {kind:'item',label:'서식 지우기',shortcut:'Ctrl+\\',disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'clear-format'})},
      {kind:'separator'},
      {kind:'item',label:merged?'셀 병합 해제':'셀 병합',disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'merge',merge:!merged})},
      {kind:'submenu',label:'데이터',disabled:!onMenuCommand,items:[
        {kind:'item',label:'범위 정렬…',icon:<ArrowUpAZ/>,onSelect:()=>onMenuCommand?.({command:'sort-dialog'})},
        {kind:'item',label:'필터 보기…',icon:<Filter/>,onSelect:()=>onMenuCommand?.({command:'filter'})},
        {kind:'item',label:'데이터 검증…',icon:<BadgeCheck/>,onSelect:()=>onMenuCommand?.({command:'data-validation'})},
        {kind:'item',label:'조건부 서식…',icon:<Palette/>,onSelect:()=>onMenuCommand?.({command:'conditional-format'})},
        {kind:'item',label:'이름 범위 지정…',icon:<Link2/>,onSelect:()=>onMenuCommand?.({command:'named-range'})},
      ]},
      {kind:'submenu',label:'삽입',disabled:!onMenuCommand,items:[
        {kind:'item',label:'차트 만들기…',icon:<BarChart3/>,onSelect:()=>onMenuCommand?.({command:'chart'})},
        {kind:'item',label:'피벗 테이블 만들기…',icon:<Table2/>,onSelect:()=>onMenuCommand?.({command:'pivot'})},
        {kind:'item',label:'댓글 추가',icon:<MessageSquarePlus/>,onSelect:()=>onMenuCommand?.({command:'comment'})},
      ]},
      {kind:'separator'},
      {kind:'item',label:'셀 링크 복사',icon:<Link2/>,onSelect:()=>void copySelectionLink()},
    ]
  }
  const axisMenuItems=(axis:'row'|'column',index:number,span:{start:number;count:number}):MenuItem[]=>{
    const isColumn=axis==='column'
    const first=span.start,count=span.count
    const label=isColumn?count>1?`열 ${columnName(first)}–${columnName(first+count-1)}`:`열 ${columnName(first)}`:count>1?`행 ${first}–${first+count-1}`:`행 ${first}`
    const unit=isColumn?'열':'행'
    return [{kind:'label',label},...clipboardMenuItems(),{kind:'separator'},
      {kind:'item',label:isColumn?`왼쪽에 열 ${count}개 삽입`:`위에 행 ${count}개 삽입`,disabled:!onStructure,onSelect:()=>void applyStructureCommand({axis,action:'insert',index:first,count})},
      {kind:'item',label:isColumn?`오른쪽에 열 ${count}개 삽입`:`아래에 행 ${count}개 삽입`,disabled:!onStructure,onSelect:()=>void applyStructureCommand({axis,action:'insert',index:first+count,count})},
      {kind:'item',label:`${label} 삭제`,icon:<Trash2/>,danger:true,disabled:!onStructure,onSelect:()=>{if(window.confirm(`${label}을(를) 삭제할까요?`))void applyStructureCommand({axis,action:'delete',index:first,count})}},
      {kind:'item',label:`${label} 내용 지우기`,icon:<Eraser/>,onSelect:()=>void clearSelection()},
      {kind:'separator'},
      {kind:'item',label:isColumn?'열 너비 자동 맞춤':'행 높이 자동 맞춤',disabled:!onLayout,onSelect:()=>autoFit({axis,index})},
      {kind:'item',label:isColumn?'열 너비 지정…':'행 높이 지정…',disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'layout-dialog'})},
      {kind:'item',label:`${label} 숨기기`,icon:<EyeOff/>,disabled:!onLayout,onSelect:()=>void applyLayoutCommand({action:'hide',axis,start:first,count})},
      {kind:'item',label:`모든 ${unit} 표시`,disabled:!onLayout,onSelect:()=>void applyLayoutCommand({action:'show_all',axis})},
      {kind:'item',label:isColumn?`${columnName(index)}열까지 고정`:`${index}행까지 고정`,icon:<PanelTop/>,disabled:!onLayout,onSelect:()=>void applyLayoutCommand({action:'freeze',frozen_rows:isColumn?frozenRows:index,frozen_columns:isColumn?index:frozenColumns})},
      {kind:'item',label:'고정 해제',disabled:!onLayout||(frozenRows===0&&frozenColumns===0),onSelect:()=>void applyLayoutCommand({action:'freeze',frozen_rows:0,frozen_columns:0})},
      ...(isColumn?[{kind:'separator'} as MenuItem,
        {kind:'item',label:'이 열 기준 오름차순 정렬',icon:<ArrowUpAZ/>,disabled:!onMenuCommand,onSelect:()=>sortByColumn(index,'asc')} as MenuItem,
        {kind:'item',label:'이 열 기준 내림차순 정렬',icon:<ArrowDownAZ/>,disabled:!onMenuCommand,onSelect:()=>sortByColumn(index,'desc')} as MenuItem,
        {kind:'item',label:'필터 보기…',icon:<Filter/>,disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'filter'})} as MenuItem,
      ]:[]),
      {kind:'separator'},
      {kind:'item',label:'조건부 서식…',icon:<Palette/>,disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'conditional-format'})},
      {kind:'item',label:'서식 지우기',shortcut:'Ctrl+\\',disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'clear-format'})},
    ]
  }
  const cornerMenuItems=():MenuItem[]=>[
    {kind:'item',label:'전체 선택',shortcut:'Ctrl+A',onSelect:()=>selectSpan(1,1,TOTAL_ROWS,TOTAL_COLUMNS,{row:1,column:1})},
    {kind:'item',label:'붙여넣기',shortcut:'Ctrl+V',icon:<ClipboardPaste/>,onSelect:()=>void pasteFromClipboard(false)},
    {kind:'separator'},
    {kind:'item',label:'모든 행 표시',icon:<Rows3/>,disabled:!onLayout,onSelect:()=>void applyLayoutCommand({action:'show_all',axis:'row'})},
    {kind:'item',label:'모든 열 표시',disabled:!onLayout,onSelect:()=>void applyLayoutCommand({action:'show_all',axis:'column'})},
    {kind:'item',label:'고정 해제',icon:<PanelTop/>,disabled:!onLayout||(frozenRows===0&&frozenColumns===0),onSelect:()=>void applyLayoutCommand({action:'freeze',frozen_rows:0,frozen_columns:0})},
    {kind:'separator'},
    {kind:'item',label:'시트 레이아웃…',disabled:!onMenuCommand,onSelect:()=>onMenuCommand?.({command:'layout-dialog'})},
  ]
  const openContextMenu=(event:React.MouseEvent<HTMLCanvasElement>)=>{
    event.preventDefault()
    const{x,y}=pointerPosition(event),region=regionAt(x,y)
    viewport.current?.focus()
    if(region.kind==='corner'){selectSpan(1,1,TOTAL_ROWS,TOTAL_COLUMNS,{row:1,column:1});setMenu({x:event.clientX,y:event.clientY,items:cornerMenuItems(),label:'시트 전체 메뉴'});return}
    if(region.kind==='column'){
      const inside=wholeColumnsSelected&&region.index>=selection.startColumn&&region.index<=selection.endColumn
      if(!inside)selectSpan(1,region.index,TOTAL_ROWS,region.index,{row:1,column:region.index})
      const span=inside?{start:selection.startColumn,count:selection.endColumn-selection.startColumn+1}:{start:region.index,count:1}
      setMenu({x:event.clientX,y:event.clientY,items:axisMenuItems('column',region.index,span),label:'열 메뉴'});return
    }
    if(region.kind==='row'){
      const inside=wholeRowsSelected&&region.index>=selection.startRow&&region.index<=selection.endRow
      if(!inside)selectSpan(region.index,1,region.index,TOTAL_COLUMNS,{row:region.index,column:1})
      const span=inside?{start:selection.startRow,count:selection.endRow-selection.startRow+1}:{start:region.index,count:1}
      setMenu({x:event.clientX,y:event.clientY,items:axisMenuItems('row',region.index,span),label:'행 메뉴'});return
    }
    const outside=region.row<selection.startRow||region.row>selection.endRow||region.column<selection.startColumn||region.column>selection.endColumn
    if(outside)selectCell(region.row,region.column)
    const range=outside?{startRow:region.row,startColumn:region.column,endRow:region.row,endColumn:region.column}:selection
    setMenu({x:event.clientX,y:event.clientY,items:cellMenuItems(range),label:'셀 메뉴'})
  }
  const activeMerge=cellMerge(activeCell),inputStartRow=activeMerge?.startRow??activeRow,inputStartColumn=activeMerge?.startColumn??activeColumn,inputEndRow=activeMerge?.endRow??activeRow,inputEndColumn=activeMerge?.endColumn??activeColumn
  const inputVisibleStart=rowAxis.firstVisibleAtOrAfter(inputStartRow),inputVisibleColumn=columnAxis.firstVisibleAtOrAfter(inputStartColumn),inputLeft=HEADER_WIDTH+axisViewportPosition(columnAxis,inputVisibleColumn,scroll.left,frozenColumns),inputTop=HEADER_HEIGHT+axisViewportPosition(rowAxis,inputVisibleStart,scroll.top,frozenRows),inputWidth=columnAxis.rangeSize(inputStartColumn,inputEndColumn),inputHeight=rowAxis.rangeSize(inputStartRow,inputEndRow)
  const dropdown=!activeCell?.spill_source&&activeValidation?.rule_type==='list'&&activeValidation.show_dropdown?activeValidation:undefined
  const selectionAddress=selection.startRow===selection.endRow&&selection.startColumn===selection.endColumn?address(activeRow,activeColumn):`${address(selection.startRow,selection.startColumn)}:${address(selection.endRow,selection.endColumn)}`
  return <div className="grid-viewport" ref={viewport} tabIndex={0} onScroll={(event)=>setScroll({left:event.currentTarget.scrollLeft,top:event.currentTarget.scrollTop})} onKeyDown={keyDown} onCopy={copy} onCut={cut} onPaste={paste} aria-label="스프레드시트 그리드">
    <div className="grid-spacer" style={{width:HEADER_WIDTH+columnAxis.extent,height:HEADER_HEIGHT+rowAxis.extent}}><canvas ref={canvas} className="grid-canvas" data-conditional-cells={conditionalCells.size} onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp} onPointerCancel={pointerCancel} onDoubleClick={doubleClick} onContextMenu={openContextMenu}/></div>
    {menu&&<ContextMenu x={menu.x} y={menu.y} items={menu.items} label={menu.label} onClose={()=>{setMenu(undefined);viewport.current?.focus()}}/>}
    {dropdown&&!editing&&<button className="cell-dropdown-trigger" aria-label={`${selectionAddress} 드롭다운 열기`} title={dropdown.help_text||'드롭다운 선택'} style={{left:inputLeft+inputWidth-23,top:inputTop,width:22,height:inputHeight}} onClick={()=>setEditing(true)}>▾</button>}
    {editing&&dropdown?<div className="cell-dropdown" role="listbox" aria-label={`${selectionAddress} 드롭다운`} style={{left:inputLeft,top:inputTop+inputHeight,minWidth:Math.max(inputWidth,180)}}>{dropdown.options?.map((option,index)=><button role="option" aria-selected={optionForValue(dropdown,activeCell?.value)===option} aria-label={`드롭다운 값 ${optionLabel(option)}`} key={index} onClick={()=>void saveCell(option.value,'',activeRow,activeColumn)}><i style={{background:option.color||'#e5e7eb'}}/><span>{optionLabel(option)}</span></button>)}<button className="cell-dropdown-cancel" onClick={()=>setEditing(false)}>취소</button></div>:editing&&<input autoFocus className="cell-editor" style={{left:inputLeft,top:inputTop,width:inputWidth,height:inputHeight}} value={draft} onChange={(event)=>setDraft(event.target.value)} onBlur={()=>void commit(draft)} onKeyDown={(event)=>{const primary=event.ctrlKey||event.metaKey;if(primary&&event.shiftKey&&event.key.toLowerCase()==='v'){pasteAsValues.current=true}else if(primary&&event.key==='Enter'){event.preventDefault();void fillDraft(draft)}else if(event.key==='Enter'){event.preventDefault();commitAndMove(event.shiftKey?-1:1,0)}else if(event.key==='Tab'){event.preventDefault();commitAndMove(0,event.shiftKey?-1:1)}else if(event.key==='Escape'){setEditing(false);setDraft(activeText)}}}/>}
    <div className="sr-only" aria-live="polite">선택 범위 {selectionAddress}, 활성 셀 값 {activeText||'비어 있음'}{activeCell?.spill_source?`, ${activeCell.spill_source} 배열 수식 결과`:''}{fillPreview?`, 자동 채우기 미리보기 ${address(fillPreview.startRow,fillPreview.startColumn)}:${address(fillPreview.endRow,fillPreview.endColumn)}`:''}</div>
  </div>
}
