import { describe, expect, it } from 'vitest'
import { compareNatural } from './naturalOrder'

const ordered=(values:string[])=>[...values].sort(compareNatural)

// 서버의 internal/workbook/natural_order.go 와 같은 답을 내야 한다. 정렬은
// 화면에서 먼저 반영하고 서버가 다시 확정하므로, 둘이 어긋나면 값이 튄다.
describe('compareNatural',()=>{
  it('reads the digits in a value as a number',()=>{
    expect(ordered(['2월','10월','1월','12월','3월'])).toEqual(['1월','2월','3월','10월','12월'])
    expect(ordered(['항목10','항목2','항목1'])).toEqual(['항목1','항목2','항목10'])
    expect(ordered(['a10b','a2b','a2a'])).toEqual(['a2a','a2b','a10b'])
  })

  it('leaves text without digits alone',()=>{
    expect(ordered(['나','가','다'])).toEqual(['가','나','다'])
    expect(compareNatural('가','가')).toBe(0)
  })

  it('keeps a steady order for the same number written differently',()=>{
    expect(ordered(['7호','07호'])).toEqual(['07호','7호'])
    expect(compareNatural('007','7')).toBeLessThan(0)
  })

  it('orders numbers too long for a float exactly',()=>{
    expect(ordered(['9999999999999999999','1111111111111111111']))
      .toEqual(['1111111111111111111','9999999999999999999'])
    expect(compareNatural('10000000000000000000','9999999999999999999')).toBeGreaterThan(0)
  })

  it('puts a digit before a letter at the same position',()=>{
    expect(compareNatural('1가','가1')).toBeLessThan(0)
  })

  it('treats a longer value as greater when it starts the same',()=>{
    expect(compareNatural('가','가나')).toBeLessThan(0)
    expect(compareNatural('가나','가')).toBeGreaterThan(0)
  })
})
