import { create } from 'zustand'
import type { Cell } from '../types'

type SaveState = 'saved'|'saving'|'offline'|'conflict'|'error'
type EditorState = {
  activeRow:number
  activeColumn:number
  anchorRow:number
  anchorColumn:number
  editing:boolean
  zoom:number
  cells:Map<string,Cell>
  saveState:SaveState
  conflicts:number
  undoStack:string[]
  redoStack:string[]
  select:(row:number,column:number,extend?:boolean)=>void
  setEditing:(editing:boolean)=>void
  setZoom:(zoom:number)=>void
  putCells:(cells:Cell[])=>void
  replaceRange:(cells:Cell[],startRow:number,startColumn:number,endRow:number,endColumn:number)=>void
  putCell:(cell:Cell)=>void
  setSaveState:(saveState:SaveState,conflicts?:number)=>void
  recordOperation:(operationId:string)=>void
  takeUndo:()=>string|undefined
  restoreUndo:(operationId:string)=>void
  completeUndo:(undoOperationId:string)=>void
  takeRedo:()=>string|undefined
  restoreRedo:(operationId:string)=>void
  completeRedo:(redoOperationId:string)=>void
  reset:()=>void
}

const key=(row:number,column:number)=>`${row}:${column}`

export const useEditorStore=create<EditorState>((set,get)=>({
  activeRow:1,activeColumn:1,anchorRow:1,anchorColumn:1,editing:false,zoom:1,cells:new Map(),saveState:'saved',conflicts:0,undoStack:[],redoStack:[],
  select:(activeRow,activeColumn,extend=false)=>set((state)=>({activeRow,activeColumn,anchorRow:extend?state.anchorRow:activeRow,anchorColumn:extend?state.anchorColumn:activeColumn,editing:false})),
  setEditing:(editing)=>set({editing}),
  setZoom:(zoom)=>set({zoom:Math.max(.5,Math.min(2,zoom))}),
  putCells:(cells)=>set((state)=>{const next=new Map(state.cells);cells.forEach((cell)=>next.set(key(cell.row,cell.column),cell));return{cells:next}}),
  replaceRange:(incoming,startRow,startColumn,endRow,endColumn)=>set((state)=>{const cells=new Map(state.cells);for(const [address,cell] of cells){if(cell.row>=startRow&&cell.row<=endRow&&cell.column>=startColumn&&cell.column<=endColumn)cells.delete(address)}incoming.forEach(cell=>cells.set(key(cell.row,cell.column),cell));return{cells}}),
  putCell:(cell)=>set((state)=>{const cells=new Map(state.cells);cells.set(key(cell.row,cell.column),cell);return{cells}}),
  setSaveState:(saveState,conflicts=0)=>set({saveState,conflicts}),
  recordOperation:(operationId)=>{if(!operationId||get().undoStack.at(-1)===operationId)return;set((state)=>({undoStack:[...state.undoStack,operationId].slice(-100),redoStack:[]}))},
  takeUndo:()=>{const stack=get().undoStack;if(stack.length===0)return;const operationId=stack[stack.length-1];set({undoStack:stack.slice(0,-1)});return operationId},
  restoreUndo:(operationId)=>set((state)=>({undoStack:[...state.undoStack,operationId].slice(-100)})),
  completeUndo:(undoOperationId)=>set((state)=>({redoStack:[...state.redoStack,undoOperationId].slice(-100)})),
  takeRedo:()=>{const stack=get().redoStack;if(stack.length===0)return;const operationId=stack[stack.length-1];set({redoStack:stack.slice(0,-1)});return operationId},
  restoreRedo:(operationId)=>set((state)=>({redoStack:[...state.redoStack,operationId].slice(-100)})),
  completeRedo:(redoOperationId)=>set((state)=>({undoStack:[...state.undoStack,redoOperationId].slice(-100)})),
  reset:()=>set({activeRow:1,activeColumn:1,anchorRow:1,anchorColumn:1,editing:false,cells:new Map(),saveState:'saved',conflicts:0,undoStack:[],redoStack:[]}),
}))

export const cellKey=key
export function selectedBounds(state:Pick<EditorState,'activeRow'|'activeColumn'|'anchorRow'|'anchorColumn'>){return{startRow:Math.min(state.anchorRow,state.activeRow),startColumn:Math.min(state.anchorColumn,state.activeColumn),endRow:Math.max(state.anchorRow,state.activeRow),endColumn:Math.max(state.anchorColumn,state.activeColumn)}}
