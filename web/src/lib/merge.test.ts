import { describe,expect,it } from 'vitest'
import type { Cell } from '../types'
import { cellMerge,mergeStyle,selectedMergedBounds,stripMergeStyle } from './merge'

const merged:Cell={sheet_id:'sheet',row:2,column:2,value:9,style:{bold:true,merge:{start_row:1,start_column:1,end_row:2,end_column:2}},updated_at:'now'}

describe('merged cell metadata',()=>{
  it('resolves a covered cell to the complete range',()=>{
    expect(cellMerge(merged)).toEqual({startRow:1,startColumn:1,endRow:2,endColumn:2})
    expect(selectedMergedBounds(new Map([['2:2',merged]]),{startRow:2,startColumn:2,endRow:2,endColumn:2})).toEqual({startRow:1,startColumn:1,endRow:2,endColumn:2})
  })

  it('adds and removes merge metadata without changing ordinary styles',()=>{
    const range={startRow:3,startColumn:4,endRow:4,endColumn:5}
    expect(mergeStyle({italic:true},range,true)).toEqual({italic:true,merge:{start_row:3,start_column:4,end_row:4,end_column:5}})
    expect(mergeStyle(merged.style,range,false)).toEqual({bold:true})
    expect(stripMergeStyle(merged.style)).toEqual({bold:true})
  })

  it('rejects malformed or non-containing metadata',()=>{
    expect(cellMerge({...merged,style:{merge:{start_row:1,start_column:1,end_row:1,end_column:1}}})).toBeUndefined()
    expect(cellMerge({...merged,style:{merge:{start_row:'1'}}})).toBeUndefined()
  })
})
