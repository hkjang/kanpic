import { create } from 'zustand'
import type { Cell, MutationResult } from '../types'

type SaveState = 'saved'|'saving'|'offline'|'conflict'|'error'
type EditorState = {
  activeRow:number
  activeColumn:number
  anchorRow:number
  anchorColumn:number
  editing:boolean
  /** Text being edited, shared by the grid editor and the formula bar. */
  draft:string
  zoom:number
  cells:Map<string,Cell>
  saveState:SaveState
  conflicts:number
  /**
   * 방금 한 편집이 만든 수식 오류. 행을 지워 다른 곳의 수식이 깨져도
   * 지금까지 아무 말이 없었다. 서버는 이미 알려 주고 있었다.
   */
  formulaIssues:MutationResult['formula_errors']
  /**
   * 되돌릴 수 없는 편집 직전에 서버가 남긴 자동 백업. 행·열 삭제는 실행
   * 취소로 되살릴 수 없어서, 이 버전으로 복원하는 것이 유일한 회수 경로다.
   */
  editBackup?:{versionId:string;summary:string}
  undoStack:string[]
  redoStack:string[]
  select:(row:number,column:number,extend?:boolean)=>void
  setEditing:(editing:boolean)=>void
  setDraft:(draft:string)=>void
  setZoom:(zoom:number)=>void
  putCells:(cells:Cell[])=>void
  replaceRange:(cells:Cell[],startRow:number,startColumn:number,endRow:number,endColumn:number)=>void
  putCell:(cell:Cell)=>void
  setSaveState:(saveState:SaveState,conflicts?:number)=>void
  reportFormulaErrors:(errors?:MutationResult['formula_errors'])=>void
  reportRecoverableEdit:(backup:{versionId:string;summary:string}|undefined,errors?:MutationResult['formula_errors'])=>void
  clearFormulaIssues:()=>void
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
  activeRow:1,activeColumn:1,anchorRow:1,anchorColumn:1,editing:false,draft:'',zoom:1,cells:new Map(),saveState:'saved',conflicts:0,formulaIssues:[],editBackup:undefined,undoStack:[],redoStack:[],
  select:(activeRow,activeColumn,extend=false)=>set((state)=>({activeRow,activeColumn,anchorRow:extend?state.anchorRow:activeRow,anchorColumn:extend?state.anchorColumn:activeColumn,editing:false})),
  setEditing:(editing)=>set({editing}),
  setDraft:(draft)=>set({draft}),
  setZoom:(zoom)=>set({zoom:Math.max(.5,Math.min(2,zoom))}),
  putCells:(cells)=>set((state)=>{const next=new Map(state.cells);cells.forEach((cell)=>next.set(key(cell.row,cell.column),cell));return{cells:next}}),
  replaceRange:(incoming,startRow,startColumn,endRow,endColumn)=>set((state)=>{const cells=new Map(state.cells);for(const [address,cell] of cells){if(cell.row>=startRow&&cell.row<=endRow&&cell.column>=startColumn&&cell.column<=endColumn)cells.delete(address)}incoming.forEach(cell=>cells.set(key(cell.row,cell.column),cell));return{cells}}),
  putCell:(cell)=>set((state)=>{const cells=new Map(state.cells);cells.set(key(cell.row,cell.column),cell);return{cells}}),
  setSaveState:(saveState,conflicts=0)=>set({saveState,conflicts}),
  // 오류가 없는 편집은 이전 안내를 지운다. 고친 뒤에도 경고가 남아 있으면
  // 무엇이 지금 문제인지 알 수 없다.
  reportFormulaErrors:(errors)=>set({formulaIssues:errors&&errors.length>0?errors:[],editBackup:undefined}),
  reportRecoverableEdit:(editBackup,errors)=>set({formulaIssues:errors&&errors.length>0?errors:[],editBackup}),
  clearFormulaIssues:()=>set({formulaIssues:[],editBackup:undefined}),
  recordOperation:(operationId)=>{if(!operationId||get().undoStack.at(-1)===operationId)return;set((state)=>({undoStack:[...state.undoStack,operationId].slice(-100),redoStack:[]}))},
  takeUndo:()=>{const stack=get().undoStack;if(stack.length===0)return;const operationId=stack[stack.length-1];set({undoStack:stack.slice(0,-1)});return operationId},
  restoreUndo:(operationId)=>set((state)=>({undoStack:[...state.undoStack,operationId].slice(-100)})),
  completeUndo:(undoOperationId)=>set((state)=>({redoStack:[...state.redoStack,undoOperationId].slice(-100)})),
  takeRedo:()=>{const stack=get().redoStack;if(stack.length===0)return;const operationId=stack[stack.length-1];set({redoStack:stack.slice(0,-1)});return operationId},
  restoreRedo:(operationId)=>set((state)=>({redoStack:[...state.redoStack,operationId].slice(-100)})),
  completeRedo:(redoOperationId)=>set((state)=>({undoStack:[...state.undoStack,redoOperationId].slice(-100)})),
  reset:()=>set({activeRow:1,activeColumn:1,anchorRow:1,anchorColumn:1,editing:false,draft:'',cells:new Map(),saveState:'saved',conflicts:0,formulaIssues:[],editBackup:undefined,undoStack:[],redoStack:[]}),
}))

export const cellKey=key
export function selectedBounds(state:Pick<EditorState,'activeRow'|'activeColumn'|'anchorRow'|'anchorColumn'>){return{startRow:Math.min(state.anchorRow,state.activeRow),startColumn:Math.min(state.anchorColumn,state.activeColumn),endRow:Math.max(state.anchorRow,state.activeRow),endColumn:Math.max(state.anchorColumn,state.activeColumn)}}
