@rules
Feature: Package journey rules from the documentation
  The journey only moves forward along the edges the Aggregate Design Canvas
  draws. A scan that cannot move it is stored, never applied and never
  announced; consumers acknowledge it instead of retrying. These scenarios pin
  every edge, the time-order and duplicate rules, the milestone events, the
  return and the privacy of the timeline (docs: aggregate-design-canvas,
  use-cases RecordScan and ReturnToSender, domain-events, ADR 0002 A5/A6/A20-A22).

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  # --- the edge table -------------------------------------------------------

  Scenario Outline: A scan moves the journey along a documented edge
    Given the package has reached "<from>"
    When I record a "<scan>" scan "t1" at hour 10
    Then the request succeeds
    And the journey is "<to>"
    And the journey has <total> scans of which <total> applied
    And the last PackageScanned event is scan "t1" of type "<scan>" at node "node-<node>" with state "<to>"

    Examples:
      | from        | scan            | node            | to               | total |
      | HANDED_OVER | TRANSIT_ARRIVAL | transit_arrival | IN_TRANSIT       | 2     |
      | IN_TRANSIT  | DEPARTURE       | departure       | IN_TRANSIT       | 3     |
      | IN_TRANSIT  | TRANSIT_ARRIVAL | transit_arrival | IN_TRANSIT       | 3     |
      | IN_TRANSIT  | STATION_ARRIVAL | station_arrival | AT_STATION       | 3     |
      | AT_STATION  | OUT_FOR_DELIVERY| out_for_delivery| OUT_FOR_DELIVERY | 4     |

  Scenario Outline: A scan with no edge from the current state is stored but never applied or announced
    Given the package has reached "<state>"
    When I record a "<scan>" scan "t1" at hour 10
    Then the request is refused with "illegal-transition"
    And the journey is "<state>"
    And the journey has <total> scans of which <applied> applied
    And the last scan is stored as not applied
    And no event was published since the package reached its state

    Examples:
      | state            | scan             | total | applied |
      | LABELLED         | DEPARTURE        | 1     | 0       |
      | LABELLED         | STATION_ARRIVAL  | 1     | 0       |
      | LABELLED         | OUT_FOR_DELIVERY | 1     | 0       |
      | LABELLED         | DELIVERY_FAILED  | 1     | 0       |
      | HANDED_OVER      | PICKUP_DEPARTURE | 2     | 1       |
      | HANDED_OVER      | SHIPMENT_CONFIRMED | 2   | 1       |
      | HANDED_OVER      | OUT_FOR_DELIVERY | 2     | 1       |
      | HANDED_OVER      | DELIVERED        | 2     | 1       |
      | IN_TRANSIT       | PICKUP_DEPARTURE | 3     | 2       |
      | IN_TRANSIT       | DELIVERY_FAILED  | 3     | 2       |
      | AT_STATION       | DEPARTURE        | 4     | 3       |
      | AT_STATION       | TRANSIT_ARRIVAL  | 4     | 3       |
      | AT_STATION       | STATION_ARRIVAL  | 4     | 3       |
      | AT_STATION       | DELIVERED        | 4     | 3       |
      | OUT_FOR_DELIVERY | STATION_ARRIVAL  | 5     | 4       |
      | OUT_FOR_DELIVERY | OUT_FOR_DELIVERY | 5     | 4       |

  Scenario: The vendor's shipment confirmation does not move a LABELLED package
    When I record a "SHIPMENT_CONFIRMED" scan "v1" at hour 1
    And I query the journey of the package
    Then the journey is "LABELLED"
    And the published events include "PackageHandedOver" 0 times
    And the timeline carries no handover or delivery instant
    And the timeline has the scans "v1" in the order they were recorded

  Scenario: The handover is the network's own pickup scan, not the vendor's statement
    Given I record a "SHIPMENT_CONFIRMED" scan "v1" at hour 1
    When a linehaul departure of kind "PICKUP" from node "fc-1" is applied at hour 3
    And I query the journey of the package
    Then the journey is "HANDED_OVER"
    And the timeline says the package was handed over at hour 3
    And the PackageHandedOver event carries purchase order "PO-1", node "fc-1" and instant hour 3

  Scenario: Only a pickup-kind departure hands the package over
    When a linehaul departure of kind "LINEHAUL" from node "fc-1" is applied at hour 3
    Then the consumed event was applied to 0, stored for 1, refused as closed for 0 and unknown for 0 packages
    And the journey is "LABELLED"
    And the journey has 1 scans of which 0 applied
    And the published events are "LabelIssued"

  Scenario: A departure that names no kind is a plain departure once the package is past LABELLED
    Given a linehaul departure of kind "PICKUP" from node "fc-1" is applied at hour 1
    When a linehaul departure of kind "" from node "fc-1" is applied at hour 3
    Then the journey is "IN_TRANSIT"
    And the published events include "PackageHandedOver" 1 times

  # --- time order and duplicates --------------------------------------------

  Scenario: A scan at the very instant of the last applied scan is still in order
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    When I record a "DEPARTURE" scan "s2" at hour 5
    Then the request succeeds
    And the journey is "IN_TRANSIT"
    And the journey has 2 scans of which 2 applied

  Scenario: Time order is judged before the edge table
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    When I record a "DELIVERED" scan "late-news" at hour 2
    Then the request is refused with "scan-out-of-order"
    And the journey has 2 scans of which 1 applied

  Scenario: An out-of-order scan does not rewind the last applied instant
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    And I record a "DEPARTURE" scan "old-1" at hour 2
    When I record a "DEPARTURE" scan "old-2" at hour 4
    Then the request is refused with "scan-out-of-order"
    And the journey has 3 scans of which 1 applied

  Scenario: A refused scan raises no event
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    When I record a "DEPARTURE" scan "old" at hour 2
    And I record a "DELIVERED" scan "x1" at hour 6
    Then the published events are "LabelIssued,PackageScanned,PackageHandedOver"

  Scenario: A stored scan keeps its id, so sending it again is a duplicate
    Given I record a "DELIVERED" scan "x1" at hour 1
    When I record a "DELIVERED" scan "x1" at hour 2
    Then the request is refused with "duplicate-scan"
    And the journey has 1 scans of which 0 applied

  Scenario Outline: A malformed scan is refused and stored nowhere
    When I record a "<scanType>" scan "<scanId>" at node "<node>" at hour 3
    Then the request is refused with "validation-failed"
    And the journey has 0 scans of which 0 applied

    Examples:
      | scanType  | scanId | node       |
      | TELEPORT  | s1     | node-1     |
      | DEPARTURE |        | node-1     |
      | DEPARTURE | -s1    | node-1     |
      | DEPARTURE | s1     | bad node   |

  # --- consumed facts -------------------------------------------------------

  Scenario: A consumed scan is stored under the event id and the tracking number
    Given the package has reached "HANDED_OVER"
    When a consumed event "ev-load-77" carries a "DEPARTURE" fact at hour 5 for every package
    And I query the journey of the package
    Then the journey is "IN_TRANSIT"
    And the last scan of the timeline has the id of event "ev-load-77" and the tracking number

  Scenario: A fact without an edge is acknowledged, stored and does not move the state
    Given the package has reached "IN_TRANSIT"
    When a consumed event "ev-ofd-1" carries a "OUT_FOR_DELIVERY" fact at hour 6 for every package
    Then the request succeeds
    And the consumed event was applied to 0, stored for 1, refused as closed for 0 and unknown for 0 packages
    And the journey is "IN_TRANSIT"
    And the journey has 3 scans of which 2 applied
    And no event was published since the package reached its state

  Scenario: A late fact is stored, acknowledged and not applied
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    When a consumed event "ev-late" carries a "DEPARTURE" fact at hour 2 for every package
    Then the request succeeds
    And the consumed event was applied to 0, stored for 1, refused as closed for 0 and unknown for 0 packages
    And the journey is "HANDED_OVER"
    And the journey has 2 scans of which 1 applied

  Scenario: A fact for a delivered package is acknowledged and changes nothing
    Given a package that ended as "DELIVERED"
    When a consumed event "ev-after" carries a "DEPARTURE" fact at hour 30 for every package
    Then the request succeeds
    And the consumed event was applied to 0, stored for 0, refused as closed for 1 and unknown for 0 packages
    And the journey is "DELIVERED"
    And the last published events are "PackageScanned,PackageDelivered"

  Scenario: A fact for a package this service never labelled is acknowledged and counted
    Given the package has reached "HANDED_OVER"
    When a consumed event "ev-mixed" carries a "DEPARTURE" fact at hour 5 for every package and an unknown package
    Then the request succeeds
    And the consumed event was applied to 1, stored for 0, refused as closed for 0 and unknown for 1 packages
    And the journey is "IN_TRANSIT"

  Scenario: One load event gives each package the outcome of its own journey
    Given the package has reached "HANDED_OVER"
    And a second label has been issued for package 2 of "PO-1"
    When a consumed event "ev-two" carries a "DEPARTURE" fact at hour 5 for every package
    Then the consumed event was applied to 1, stored for 1, refused as closed for 0 and unknown for 0 packages
    And the journey is "IN_TRANSIT"

  Scenario: A redelivered event is skipped and stores nothing twice
    Given the package has reached "HANDED_OVER"
    And a consumed event "ev-dup" carries a "DEPARTURE" fact at hour 5 for every package
    When a consumed event "ev-dup" carries a "DEPARTURE" fact at hour 5 for every package
    Then the consumed event was skipped as already processed
    And the journey has 2 scans of which 2 applied

  # --- milestones and events -------------------------------------------------

  Scenario: Every applied scan publishes the state the journey is in after it
    When I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    And I record a "TRANSIT_ARRIVAL" scan "s2" at hour 3
    And I record a "STATION_ARRIVAL" scan "s3" at hour 5
    And I record a "OUT_FOR_DELIVERY" scan "s4" at hour 6
    And I record a "DELIVERED" scan "s5" at hour 8
    Then the PackageScanned events show the states "HANDED_OVER,IN_TRANSIT,AT_STATION,OUT_FOR_DELIVERY,DELIVERED"
    And the PackageHandedOver event carries purchase order "PO-1", node "node-pickup_departure" and instant hour 1
    And the PackageDelivered event carries order "ord-1", assignment "asg-1" and instant hour 8

  Scenario: A failed attempt is not an exception
    Given the package has reached "OUT_FOR_DELIVERY"
    When I record a "DELIVERY_FAILED" scan "f1" at hour 8
    Then the request succeeds
    And the journey is "AT_STATION"
    And the last PackageScanned event is scan "f1" of type "DELIVERY_FAILED" at node "node-delivery_failed" with state "AT_STATION"
    And the published events include "PackageExceptionRaised" 0 times
    And the last published events are "PackageScanned"

  Scenario: A package that failed once is delivered on the next wave
    Given the package has reached "OUT_FOR_DELIVERY"
    And I record a "DELIVERY_FAILED" scan "f1" at hour 8
    And I record a "OUT_FOR_DELIVERY" scan "o2" at hour 30
    When I record a "DELIVERED" scan "d1" at hour 32
    And I query the journey of the package
    Then the journey is "DELIVERED"
    And the timeline says the package was delivered at hour 32
    And the PackageDelivered event carries order "ord-1", assignment "asg-1" and instant hour 32
    And the published events include "PackageDelivered" 1 times

  # --- return to sender -------------------------------------------------------

  Scenario: A return ends the journey at the instant last-mile gives
    Given the package has reached "AT_STATION"
    When last-mile asks to return the package
    Then the request succeeds
    And the journey is "RETURNED"
    And the PackageReturned event carries order "ord-1" and instant hour 50

  Scenario: A return that arrives before the failed-attempt scan succeeds once the scan lands
    Given the package has reached "OUT_FOR_DELIVERY"
    When last-mile asks to return the package
    Then the request is refused with "illegal-transition"
    And the journey is "OUT_FOR_DELIVERY"
    When I record a "DELIVERY_FAILED" scan "f1" at hour 20
    And last-mile asks to return the package
    Then the request succeeds
    And the journey is "RETURNED"
    And the published events include "PackageReturned" 1 times

  Scenario: A redelivered return event ends the journey once
    Given the package has reached "AT_STATION"
    When last-mile asks to return the package
    And last-mile asks to return the package
    Then the request succeeds
    And the published events include "PackageReturned" 1 times

  Scenario: A return for a delivered package is acknowledged and changes nothing
    Given a package that ended as "DELIVERED"
    When last-mile asks to return the package
    Then the request succeeds
    And the journey is "DELIVERED"
    And the published events include "PackageReturned" 0 times

  Scenario: A return for an unknown package is acknowledged
    When last-mile asks to return the unknown package
    Then the request succeeds
    And the journey is "LABELLED"

  # --- end states -------------------------------------------------------------

  Scenario Outline: A journey that has ended refuses every scan
    Given a package that ended as "<end>"
    When I record a "OUT_FOR_DELIVERY" scan "late" at hour 90
    Then the request is refused with "journey-closed"
    And the journey is "<end>"

    Examples:
      | end       |
      | RETURNED  |
      | LOST      |
      | VOIDED    |

  Scenario Outline: A journey that has ended is never swept for silence
    Given a package that ended as "<end>"
    When the clock moves to hour 900
    And the silence sweep runs
    Then the sweep raised 0 exceptions and declared 0 packages lost
    And the journey is "<end>"

    Examples:
      | end       |
      | DELIVERED |
      | RETURNED  |
      | LOST      |
      | VOIDED    |

  # --- the timeline ------------------------------------------------------------

  Scenario: A fresh journey names its package and has an empty timeline
    When I query the journey of the package
    Then the timeline state is "LABELLED"
    And the timeline names purchase order "PO-1", order "ord-1", assignment "asg-1" and tier "NEXT_DAY"
    And the timeline has 0 scans of which 0 applied
    And the timeline carries no handover or delivery instant

  Scenario: Nothing published over a whole delivery carries the address
    Given the package is out for delivery
    And I record a "DELIVERED" scan "d1" at hour 12
    When I query the journey of the package
    Then no published event or timeline contains any part of the address
