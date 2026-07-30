import type { Cell } from '../types'
import { cellKey } from '../state/editor'

export type MergeRange = { startRow:number; startColumn:number; endRow:number; endColumn:number }

type StoredMerge = { start_row:number; start_column:number; end_row:number; end_column:number }

export function cellMerge(cell:Cell|undefined):MergeRange|undefined {
  const raw=cell?.style?.merge
  if(!raw||typeof raw!=='object')return
  const value=raw as Partial<StoredMerge>
  if(!Number.isInteger(value.start_row)||!Number.isInteger(value.start_column)||!Number.isInteger(value.end_row)||!Number.isInteger(value.end_column))return
  const range={startRow:value.start_row!,startColumn:value.start_column!,endRow:value.end_row!,endColumn:value.end_column!}
  if(range.startRow<1||range.startColumn<1||range.endRow<range.startRow||range.endColumn<range.startColumn||!cell||cell.row<range.startRow||cell.row>range.endRow||cell.column<range.startColumn||cell.column>range.endColumn)return
  return range
}

export function selectedMergedBounds(cells:Map<string,Cell>,selection:MergeRange):MergeRange {
  if(selection.startRow!==selection.endRow||selection.startColumn!==selection.endColumn)return selection
  return cellMerge(cells.get(cellKey(selection.startRow,selection.startColumn)))??selection
}

export function mergeStyle(style:Record<string,unknown>|undefined,range:MergeRange,merge:boolean){
  const next={...(style??{})}
  if(merge)next.merge={start_row:range.startRow,start_column:range.startColumn,end_row:range.endRow,end_column:range.endColumn}
  else delete next.merge
  return Object.keys(next).length?next:undefined
}

export function stripMergeStyle(style:Record<string,unknown>|undefined){
  if(!style||style.merge===undefined)return style
  const next={...style}
  delete next.merge
  return Object.keys(next).length?next:undefined
}
