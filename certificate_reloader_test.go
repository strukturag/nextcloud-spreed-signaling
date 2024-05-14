/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
 *
 * @author Joachim Bauch <bauch@struktur.de>
 *
 * @license GNU AGPL version 3 or any later version
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */
package signaling

import (
	"context"
	"testing"
	"time"
)

func UpdateCertificateCheckIntervalForTest(t *testing.T, interval time.Duration) {
	t.Helper()
	// Make sure test is not executed with "t.Parallel()"
	t.Setenv("PARALLEL_CHECK", "1")
	old := deduplicateWatchEvents.Load()
	t.Cleanup(func() {
		deduplicateWatchEvents.Store(old)
	})

	deduplicateWatchEvents.Store(int64(interval))
}

func (r *CertificateReloader) WaitForReload(ctx context.Context) error {
	counter := r.GetReloadCounter()
	for counter == r.GetReloadCounter() {
		if err := ctx.Err(); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

func (r *CertPoolReloader) WaitForReload(ctx context.Context) error {
	counter := r.GetReloadCounter()
	for counter == r.GetReloadCounter() {
		if err := ctx.Err(); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}
