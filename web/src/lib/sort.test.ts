import { describe,expect,it } from 'vitest'
import type { Cell } from '../types'
import { materializeSort } from './sort'

const cell=(row:number,column:number,value:unknown,formula?:string,style?:Record<string,unknown>):Cell=>({sheet_id:'sheet',row,column,value,formula,style,updated_at:'now'})

describe('range sorting',()=>{
  it('applies stable multi-key sorting and shifts formulas with styles',()=>{
    const cells=new Map<string,Cell>([
      ['1:1',cell(1,1,'Name')],['1:2',cell(1,2,'Quantity')],
      ['2:1',cell(2,1,'beta')],['2:2',cell(2,2,2)],['2:3',cell(2,3,20,'=B2*10',{bold:true})],
      ['3:1',cell(3,1,'Alpha')],['3:2',cell(3,2,10)],['3:3',cell(3,3,100,'=B3*10')],
      ['4:1',cell(4,1,'alpha')],['4:2',cell(4,2,5)],['4:3',cell(4,3,50,'=B4*10')],
    ])
    const result=materializeSort(cells,{startRow:1,startColumn:1,endRow:4,endColumn:3},{headerRows:1,caseSensitive:false,keys:[{column:1,direction:'asc'},{column:2,direction:'desc'}]},'sheet')
    expect([result[0].value,result[3].value,result[6].value]).toEqual(['Alpha','alpha','beta'])
    expect([result[2].formula,result[5].formula,result[8].formula]).toEqual(['=B2*10','=B3*10','=B4*10'])
    expect(result[8].style).toEqual({bold:true})
  })

  it('keeps blanks last for descending sort',()=>{
    const cells=new Map<string,Cell>([['1:1',cell(1,1,2)],['3:1',cell(3,1,9)],['4:1',cell(4,1,1)]])
    const result=materializeSort(cells,{startRow:1,startColumn:1,endRow:4,endColumn:1},{headerRows:0,caseSensitive:false,keys:[{column:1,direction:'desc'}]},'sheet')
    expect(result.map(item=>item.value)).toEqual([9,2,1,undefined])
  })

  it('rejects duplicate keys, merged cells, and oversized operations',()=>{
    const range={startRow:1,startColumn:1,endRow:3,endColumn:2}
    expect(()=>materializeSort(new Map(),range,{headerRows:0,caseSensitive:false,keys:[{column:1,direction:'asc'},{column:1,direction:'desc'}]},'sheet')).toThrow('중복 없이')
    const merged=cell(1,1,'x',undefined,{merge:{start_row:1,start_column:1,end_row:2,end_column:1}})
    expect(()=>materializeSort(new Map([['1:1',merged]]),range,{headerRows:0,caseSensitive:false,keys:[{column:1,direction:'asc'}]},'sheet')).toThrow('병합 해제')
    expect(()=>materializeSort(new Map(),{startRow:1,startColumn:1,endRow:5001,endColumn:2},{headerRows:0,caseSensitive:false,keys:[{column:1,direction:'asc'}]},'sheet')).toThrow('10,000셀')
  })
})
