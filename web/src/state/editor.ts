import { create } from 'zustand'
import type { Cell } from '../types'

type SaveState = 'saved'|'saving'|'offline'|'conflict'|'error'
type EditorState = {
  activeRow:number
  activeColumn:number
  editing:boolean
  zoom:number
  cells:Map<string,Cell>
  saveState:SaveState
  conflicts:number
  select:(row:number,column:number)=>void
  setEditing:(editing:boolean)=>void
  setZoom:(zoom:number)=>void
  putCells:(cells:Cell[])=>void
  putCell:(cell:Cell)=>void
  setSaveState:(saveState:SaveState,conflicts?:number)=>void
  reset:()=>void
}

const key=(row:number,column:number)=>`${row}:${column}`

export const useEditorStore=create<EditorState>((set)=>({
  activeRow:1,activeColumn:1,editing:false,zoom:1,cells:new Map(),saveState:'saved',conflicts:0,
  select:(activeRow,activeColumn)=>set({activeRow,activeColumn,editing:false}),
  setEditing:(editing)=>set({editing}),
  setZoom:(zoom)=>set({zoom:Math.max(.5,Math.min(2,zoom))}),
  putCells:(cells)=>set((state)=>{const next=new Map(state.cells);cells.forEach((cell)=>next.set(key(cell.row,cell.column),cell));return{cells:next}}),
  putCell:(cell)=>set((state)=>{const cells=new Map(state.cells);cells.set(key(cell.row,cell.column),cell);return{cells}}),
  setSaveState:(saveState,conflicts=0)=>set({saveState,conflicts}),
  reset:()=>set({activeRow:1,activeColumn:1,editing:false,cells:new Map(),saveState:'saved',conflicts:0}),
}))

export const cellKey=key
