import { describe, it, expect } from 'vitest';
import { mbps, occupancy, percentUsed } from '@/api/elx-management';
describe('ElX management units and session counts', () => {
  it('converts B/s into bit/s', () => {
    expect(mbps(12_500_000)).toContain('100');
  });
  it('counts occupied slots, not registered devices', () => {
    expect(
      occupancy({
        id: 'x',
        activeSlots: 2,
        occupied: 1,
        validUntil: '',
        enabled: true,
        devices: 5,
      }),
    ).toBe('1 / 2');
  });
  it('respects FUP traffic direction', () => {
    const p = {
      id: 'x',
      startsAt: '',
      endsAt: '',
      direction: 'down' as const,
      thresholdBytes: 1000,
      upBytes: 2000,
      downBytes: 600,
      reducedUp: 100,
      reducedDown: 100,
      fupActive: false,
    };
    expect(percentUsed(p)).toBe(60);
    expect(percentUsed({ ...p, direction: 'total' })).toBe(100);
    expect(percentUsed({ ...p, direction: 'up' })).toBe(100);
  });
});
